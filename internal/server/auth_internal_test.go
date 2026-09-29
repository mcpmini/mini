package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
)

func newInternalAuthTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	return New(Params{Config: cfg, ConfigDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

func TestCancelExistingAuthFlow_closesListenerSynchronously(t *testing.T) {
	srv := newInternalAuthTestServer(t)

	login := authtest.StartLogin(t, &config.AuthConfig{})

	cancelled := false
	srv.authMu.Lock()
	srv.authFlows["srv1"] = &authFlowState{cancel: func() { cancelled = true }, login: login}
	srv.authMu.Unlock()

	srv.cancelExistingAuthFlow("srv1")

	if !cancelled {
		t.Error("expected cancel() to be called")
	}

	authtest.RequireCallbackPortReleased(t, login)

	srv.authMu.Lock()
	_, exists := srv.authFlows["srv1"]
	srv.authMu.Unlock()
	if exists {
		t.Error("expected authFlows entry to be removed after cancel")
	}
}

func TestRunAuthFlow_staleCleanupPreservesNewerFlow(t *testing.T) {
	srv := newInternalAuthTestServer(t)

	staleLogin := authtest.StartLogin(t, &config.AuthConfig{})

	_, staleCancel := context.WithCancel(context.Background())
	defer staleCancel()
	staleFlow := &authFlowState{cancel: staleCancel, login: staleLogin}

	activeLogin := authtest.StartLogin(t, &config.AuthConfig{})

	_, activeCancel := context.WithCancel(context.Background())
	defer activeCancel()
	activeFlow := &authFlowState{cancel: activeCancel, login: activeLogin}

	srv.authMu.Lock()
	srv.authFlows["svc"] = activeFlow
	srv.authMu.Unlock()

	authCtx, cancel := context.WithCancel(context.Background())
	cancel()

	srv.authWg.Add(1)
	srv.runAuthFlow(authCtx, config.ServerConfig{Name: "svc"}, staleFlow)

	srv.authMu.Lock()
	got := srv.authFlows["svc"]
	srv.authMu.Unlock()
	if got != activeFlow {
		t.Fatalf("stale cleanup removed newer flow: got %p want %p", got, activeFlow)
	}
}
