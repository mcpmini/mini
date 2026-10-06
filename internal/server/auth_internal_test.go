//go:build test

package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
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

func TestRemoveServer_closesPendingLogin(t *testing.T) {
	srv := newInternalAuthTestServer(t)
	login := authtest.StartLogin(t, &config.AuthConfig{})
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.storeAuthFlow("svc", &authFlowState{cancel: cancel, login: login})

	if _, err := srv.removeServerFromAgent("svc"); err != nil {
		t.Fatal(err)
	}

	authtest.RequireCallbackPortReleased(t, login)
	srv.authMu.Lock()
	_, pending := srv.authFlows["svc"]
	srv.authMu.Unlock()
	if pending {
		t.Error("login still pending after remove_server")
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
	srv.runAuthFlow(authCtx, srv.replacingInstall(config.ServerConfig{Name: "svc"}), staleFlow)

	srv.authMu.Lock()
	got := srv.authFlows["svc"]
	srv.authMu.Unlock()
	if got != activeFlow {
		t.Fatalf("stale cleanup removed newer flow: got %p want %p", got, activeFlow)
	}
}

func TestRunAuthFlow_loginCompletingAfterRemoveServerDoesNotReinstall(t *testing.T) {
	echomcp := os.Getenv("ECHOMCP_BIN")
	if echomcp == "" {
		t.Fatal(
			"ECHOMCP_BIN not set; run check.sh or: go build -o /tmp/echomcp ./cmd/echomcp && ECHOMCP_BIN=/tmp/echomcp go test ...",
		)
	}
	for _, tc := range []struct {
		name      string
		remove    bool
		wantTools bool
	}{
		{name: "not removed: the login installs the server", remove: false, wantTools: true},
		{name: "removed after start_auth: the login does not", remove: true, wantTools: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newInternalAuthTestServer(t)
			t.Cleanup(srv.Close)
			ac := authtest.NewTokenServer(t).AuthConfig()
			installAtStartAuth := srv.replacingInstall(config.ServerConfig{Name: "svc", Command: echomcp, Auth: ac})
			login := authtest.StartLogin(t, ac)

			if tc.remove {
				if _, err := srv.removeServerFromAgent("svc"); err != nil {
					t.Fatal(err)
				}
			}
			auth.BufferCallbackCode(login, "test-auth-code")
			srv.authWg.Add(1)
			srv.runAuthFlow(t.Context(), installAtStartAuth, &authFlowState{cancel: func() {}, login: login})

			if got := len(srv.reg.All()) > 0; got != tc.wantTools {
				t.Errorf("tools registered = %v, want %v", got, tc.wantTools)
			}
		})
	}
}
