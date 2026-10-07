//go:build test

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
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
	echomcp := testutil.Binary(t, "ECHOMCP_BIN")
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

func TestRunAuthFlow_CloseCancelsReconnectAfterLogin(t *testing.T) {
	srv := newInternalAuthTestServer(t)
	t.Cleanup(srv.Close)
	url, initializing := stalledAuthInitialize(t)
	ctx := startCompletedAuthFlow(t, srv, config.ServerConfig{Name: "svc", Transport: "http", URL: url})
	select {
	case <-initializing:
	case <-time.After(time.Second):
		t.Fatal("OAuth reconnect did not reach upstream initialize")
	}

	closed := make(chan struct{})
	go func() { srv.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not complete while upstream initialize remained blocked")
	}
	if ctx.Err() != context.Canceled {
		t.Fatal("Close did not cancel the auth flow")
	}
	if got := len(srv.reg.All()); got != 0 {
		t.Errorf("registered tools after Close = %d, want 0", got)
	}
	if srv.isUpstreamRegistered("svc") {
		t.Error("server remained registered after Close")
	}
}

func stalledAuthInitialize(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	initializing, release := make(chan struct{}, 1), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case initializing <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(func() { close(release); upstream.Close() })
	return upstream.URL, initializing
}

func startCompletedAuthFlow(t *testing.T, srv *Server, sc config.ServerConfig) context.Context {
	t.Helper()
	sc.Auth = authtest.NewTokenServer(t).AuthConfig()
	login := authtest.StartLogin(t, sc.Auth)
	auth.BufferCallbackCode(login, "test-auth-code")
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	flow := &authFlowState{cancel: cancel, login: login}
	srv.storeAuthFlow(sc.Name, flow)
	install := srv.replacingInstall(sc)
	srv.authWg.Add(1)
	go srv.runAuthFlow(ctx, install, flow)
	return ctx
}
