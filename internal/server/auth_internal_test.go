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
	return NewWithConfigDir(cfg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCancelExistingAuthFlow_closesListenerSynchronously(t *testing.T) {
	srv := newInternalAuthTestServer(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	cancelled := false
	srv.authMu.Lock()
	srv.authFlows["srv1"] = &authFlowState{cancel: func() { cancelled = true }, listener: ln}
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
	_, staleCancel := context.WithCancel(context.Background())
	defer staleCancel()

	staleFlow := &authFlowState{cancel: staleCancel}
	_, activeCancel := context.WithCancel(context.Background())
	defer activeCancel()
	activeFlow := &authFlowState{cancel: activeCancel}

	srv.authMu.Lock()
	srv.authFlows["svc"] = activeFlow
	srv.authMu.Unlock()
	doneCh := make(chan auth.PKCEResult, 1)
	doneCh <- auth.PKCEResult{Err: context.Canceled}
	srv.authWg.Add(1)
	srv.runAuthFlow("svc", config.ServerConfig{Name: "svc"}, staleFlow, doneCh)
	srv.authMu.Lock()
	got := srv.authFlows["svc"]
	srv.authMu.Unlock()
	if got != activeFlow {
		t.Fatalf("stale cleanup removed newer flow: got %p want %p", got, activeFlow)
	}
}
