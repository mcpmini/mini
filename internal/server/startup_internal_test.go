//go:build test

package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestRecordStartupFailure_aStaleAttemptDoesNotMarkAServerConfiguredAgainUnderItsName(t *testing.T) {
	srv := newInstallTestServer(t)
	srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})
	stale := srv.startupInstall(config.ServerConfig{Name: "svc"})
	srv.detachAndCloseServer("svc")
	srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})

	srv.recordStartupFailure(stale, needsAuthStatus{})

	srv.stateMu.RLock()
	defer srv.stateMu.RUnlock()
	if state := srv.startup.state("svc", srv.clock.Now()); state.phase() == "failed" {
		t.Error("a failure from before the remove was recorded against the server configured again")
	}
}

func TestDetachAndCloseServer_aServerConfiguredAgainStartsWithoutTheOldStartupState(t *testing.T) {
	cases := []struct {
		name  string
		setUp func(*Server)
	}{
		{name: "was connected", setUp: func(srv *Server) {
			if err := srv.AddConnection(
				t.Context(),
				config.ServerConfig{Name: "svc"},
				&transport.FakeConnection{},
			); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "had failed", setUp: func(srv *Server) {
			srv.recordStartupFailure(
				srv.startupInstall(config.ServerConfig{Name: "svc"}),
				needsAuthStatus{},
			)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newInstallTestServer(t)
			srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})
			srv.openConnectWindow("svc")
			tc.setUp(srv)
			srv.detachAndCloseServer("svc")
			srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})
			srv.openConnectWindow("svc")

			srv.stateMu.RLock()
			defer srv.stateMu.RUnlock()
			if state := srv.startup.state("svc", srv.clock.Now()); state.phase() != "connecting" {
				t.Errorf("svc configured again is %s, want connecting", state.phase())
			}
		})
	}
}

func TestWaitForStartup_endsWhenTheRequestIsCanceled(t *testing.T) {
	srv := newInstallTestServer(t)
	srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})
	srv.openConnectWindow("svc")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := srv.waitForStartup(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("waitForStartup with a canceled request = %v, want context.Canceled", err)
	}
}

func TestWaitForStartup_aWindowOpenedDuringTheHoldDoesNotExtendIt(t *testing.T) {
	srv := newInstallTestServer(t)
	fc := clock.NewFake()
	srv.clock = fc
	srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})
	srv.openConnectWindow("svc")
	done := make(chan error, 1)
	go func() { done <- srv.waitForStartup(t.Context()) }()
	if err := fc.BlockUntilContext(t.Context(), 2); err != nil {
		t.Fatal("the hold never started its timers:", err)
	}

	fc.Advance(startupHold - time.Second)
	srv.openConnectWindow("svc")
	fc.Advance(startupHold - time.Second)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("waitForStartup = %v, want nil at its deadline", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a window reopened during the hold kept the listing waiting past startupHold")
	}
}

func TestConnectedStartupHasNoFailureDiagnostics(t *testing.T) {
	tracker := newStartupTracker()
	tracker.recordFailure("svc", needsAuthStatus{})
	tracker.markReady("svc")
	state := tracker.state("svc", time.Time{})
	if code := state.errorCode(); code != "" {
		t.Errorf("connected error code = %q, want none", code)
	}
	if reason := state.reason("svc"); reason != "" {
		t.Errorf("connected failure reason = %q, want none", reason)
	}
	if state.retryable() || state.action() != "" {
		t.Error("connected server has no startup failure to retry or authorize")
	}
}

func TestMissingStartupStatusDoesNotInventATrustFailure(t *testing.T) {
	tracker := newStartupTracker()
	tracker.recordFailure("svc", nil)
	states := []startupState{{}, tracker.state("svc", time.Time{})}
	for _, state := range states {
		if code := state.errorCode(); code != "server_failed" {
			t.Errorf("missing status error code = %q, want server_failed", code)
		}
		want := `server "svc" could not start; ask the user to run "mini status".`
		if reason := state.reason("svc"); reason != want {
			t.Errorf("missing status reason = %q, want %q", reason, want)
		}
		if state.retryable() || state.action() != "" {
			t.Error("missing startup status must not request retries or authorization")
		}
	}
}

func TestStartupPhasePreservesWireValues(t *testing.T) {
	cases := []struct {
		name   string
		status startupStatus
		phase  startupPhase
		wire   string
	}{
		{name: "connected", status: connectedStatus{}, phase: phaseConnected, wire: `{"state":"connected"}`},
		{name: "connecting", status: connectingStatus{}, phase: phaseConnecting, wire: `{"state":"connecting"}`},
		{name: "delayed", status: delayedStatus{}, phase: phaseDelayed, wire: `{"state":"delayed"}`},
		{name: "auth", status: needsAuthStatus{}, phase: phaseFailed, wire: `{"state":"failed"}`},
		{name: "environment", status: needsEnvStatus{}, phase: phaseFailed, wire: `{"state":"failed"}`},
		{name: "trust", status: notTrustedStatus{}, phase: phaseFailed, wire: `{"state":"failed"}`},
		{name: "missing", phase: phaseFailed, wire: `{"state":"failed"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			phase := (startupState{status: tc.status}).phase()
			if phase != tc.phase {
				t.Errorf("phase = %q, want %q", phase, tc.phase)
			}
			got, err := json.Marshal(map[string]any{"state": phase})
			if err != nil || string(got) != tc.wire {
				t.Errorf("wire phase = %s, %v; want %s", got, err, tc.wire)
			}
		})
	}
}
