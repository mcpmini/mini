package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

func newInternalAuthTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	return New(Params{Config: cfg, ConfigDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

func startTestLogin(t *testing.T) (*auth.BrowserLogin, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	login, err := auth.StartBrowserLogin(&config.AuthConfig{}, ln)
	if err != nil {
		t.Fatalf("StartBrowserLogin: %v", err)
	}
	return login, addr
}

func TestCancelExistingAuthFlow_closesListenerSynchronously(t *testing.T) {
	srv := newInternalAuthTestServer(t)

	login, addr := startTestLogin(t)

	cancelled := false
	srv.authMu.Lock()
	srv.authFlows["srv1"] = &authFlowState{cancel: func() { cancelled = true }, login: login}
	srv.authMu.Unlock()

	srv.cancelExistingAuthFlow("srv1")

	if !cancelled {
		t.Error("expected cancel() to be called")
	}

	// Port must be immediately reusable — no retry or sleep needed.
	ln2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("expected port to be free after cancel, got: %v", err)
	}
	ln2.Close()

	srv.authMu.Lock()
	_, exists := srv.authFlows["srv1"]
	srv.authMu.Unlock()
	if exists {
		t.Error("expected authFlows entry to be removed after cancel")
	}
}

func TestRunAuthFlow_staleCleanupPreservesNewerFlow(t *testing.T) {
	srv := newInternalAuthTestServer(t)

	staleLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	staleLogin, err := auth.StartBrowserLogin(&config.AuthConfig{}, staleLn)
	if err != nil {
		t.Fatalf("StartBrowserLogin stale: %v", err)
	}
	defer staleLogin.Close() //nolint:errcheck

	_, staleCancel := context.WithCancel(context.Background())
	defer staleCancel()
	staleFlow := &authFlowState{cancel: staleCancel, login: staleLogin}

	activeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	activeLogin, err := auth.StartBrowserLogin(&config.AuthConfig{}, activeLn)
	if err != nil {
		t.Fatalf("StartBrowserLogin active: %v", err)
	}
	defer activeLogin.Close() //nolint:errcheck

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

func TestCancelExistingAuthFlow_portReusableAfterReplace(t *testing.T) {
	srv := newInternalAuthTestServer(t)

	login, addr := startTestLogin(t)

	srv.authMu.Lock()
	srv.authFlows["svc"] = &authFlowState{cancel: func() {}, login: login}
	srv.authMu.Unlock()

	srv.cancelExistingAuthFlow("svc")

	ln2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port not reusable after replace: %v", err)
	}
	ln2.Close()
}
