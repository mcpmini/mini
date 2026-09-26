//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestReconnect_detectsOAuthRequirement(t *testing.T) {
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer oauthSrv.Close()

	configDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	fakeClock := clock.NewFake()
	srv := server.NewWithConfigDir(cfg, configDir, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithClock(fakeClock))
	defer srv.Close()

	var errOnCall bool
	srv.AddConnection(context.Background(), config.ServerConfig{
		Name: "svc", Transport: "http", URL: oauthSrv.URL,
	}, makeErrConn(&errOnCall))
	errOnCall = true
	assertEnvelopeOK(t, srv, "svc", "ping", false)

	if err := fakeClock.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatalf("waiting for reconnect timer: %v", err)
	}
	fakeClock.Advance(time.Second)

	deadline := time.Now().Add(5 * time.Second)
	for !config.IsOAuthDetected(configDir, "svc") {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the reconnect path to detect the OAuth requirement")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReconnect_reauthErrorDoesNotStartReconnect(t *testing.T) {
	srv := newTestServer(t)
	reauthErr := fmt.Errorf("svc requires re-authorization: %w", transport.ErrReauthRequired)
	errConn := &errAfterRegisterConn{
		tools: []transport.ToolDefinition{
			{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{}`)},
		},
		errFn: func() error { return reauthErr },
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, errConn)
	serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "ping", "params": map[string]any{},
	}))
	if srv.IsReconnecting("svc") {
		t.Error("ErrReauthRequired should not trigger reconnect")
	}
}

func TestReconnect_transientTokenRefreshKeepsLoop(t *testing.T) {
	const responseCleanupPlusBackoffTimers = 2
	var tokenHits atomic.Int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenHits.Add(1)
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer tokenSrv.Close()

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	configDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	fakeClock := clock.NewFake()
	srv := server.NewWithConfigDir(cfg, configDir, slog.New(slog.NewTextHandler(io.Discard, nil)),
		server.WithClock(fakeClock))
	defer srv.Close()

	tok := &oauth2.Token{AccessToken: "old-access", RefreshToken: "old-refresh"}
	if err := auth.Save(configDir, "svc", tok); err != nil {
		t.Fatal(err)
	}

	var errOnCall bool
	srv.AddConnection(context.Background(), config.ServerConfig{
		Name: "svc", Transport: "http", URL: mcpSrv.URL,
		Auth: &config.AuthConfig{Type: "oauth2", ClientID: "c", TokenURL: tokenSrv.URL},
	}, makeErrConn(&errOnCall))

	errOnCall = true
	assertEnvelopeOK(t, srv, "svc", "ping", false)

	if err := fakeClock.BlockUntilContext(t.Context(), responseCleanupPlusBackoffTimers); err != nil {
		t.Fatalf("waiting for first reconnect timer: %v", err)
	}
	fakeClock.Advance(time.Second)

	loopCtx, loopCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer loopCancel()
	if err := fakeClock.BlockUntilContext(loopCtx, responseCleanupPlusBackoffTimers); err != nil {
		t.Fatalf("reconnect loop stopped after transient token refresh failure: %v", err)
	}
	if !srv.IsReconnecting("svc") {
		t.Error("expected IsReconnecting=true after transient token refresh failure")
	}
	if hits := tokenHits.Load(); hits < 1 {
		t.Errorf("tokenHits = %d, want >= 1 (refresh path must have run)", hits)
	}
}

func TestReconnect_reauthDialFailureStopsLoop(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authSrv.Close()

	configDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	fakeClock := clock.NewFake()
	srv := server.NewWithConfigDir(cfg, configDir, slog.New(slog.NewTextHandler(io.Discard, nil)),
		server.WithClock(fakeClock))
	defer srv.Close()

	var errOnCall bool
	srv.AddConnection(context.Background(), config.ServerConfig{
		Name: "svc", Transport: "http", URL: authSrv.URL,
		Auth: &config.AuthConfig{Type: "oauth2", ClientID: "c", TokenURL: authSrv.URL + "/token"},
	}, makeErrConn(&errOnCall))

	errOnCall = true
	assertEnvelopeOK(t, srv, "svc", "ping", false)

	stopped := make(chan struct{})
	go func() {
		for srv.IsReconnecting("svc") {
			runtime.Gosched()
		}
		close(stopped)
	}()

	if err := fakeClock.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatalf("waiting for reconnect timer: %v", err)
	}
	fakeClock.Advance(time.Second)

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect loop did not stop after ErrReauthRequired")
	}
}
