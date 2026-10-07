//go:build test

package server

import (
	"context"
	"errors"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestRecordStartupFailure_aStaleAttemptDoesNotMarkAServerConfiguredAgainUnderItsName(t *testing.T) {
	srv := newInstallTestServer(t)
	srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})
	stale := srv.startupInstall(config.ServerConfig{Name: "svc"})
	srv.detachAndCloseServer("svc")
	srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})

	srv.recordStartupFailure(stale, startupFailure{kind: failureNeedsAuth})

	srv.stateMu.RLock()
	defer srv.stateMu.RUnlock()
	if state := srv.startup.state("svc", srv.clock.Now()); state.phase == phaseFailed {
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
				startupFailure{kind: failureNeedsAuth},
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
			if state := srv.startup.state("svc", srv.clock.Now()); state.phase != phaseConnecting {
				t.Errorf("svc configured again is %s, want connecting", state.phase)
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
