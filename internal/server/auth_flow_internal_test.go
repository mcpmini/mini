//go:build test

package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

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
	srv.authFlows["srv1"] = newAuthFlow(func() { cancelled = true }, login, config.ServerConfig{})
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
	srv.storeAuthFlow("svc", newAuthFlow(cancel, login, config.ServerConfig{}))

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
	staleFlow := newAuthFlow(staleCancel, staleLogin, config.ServerConfig{})

	activeLogin := authtest.StartLogin(t, &config.AuthConfig{})

	_, activeCancel := context.WithCancel(context.Background())
	defer activeCancel()
	activeFlow := newAuthFlow(activeCancel, activeLogin, config.ServerConfig{})

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
			ac.ResourceURL = "https://mcp.example.com/mcp"
			sc := config.ServerConfig{Name: "svc", Command: echomcp, URL: "https://mcp.example.com/mcp", Auth: ac}
			installAtStartAuth := srv.replacingInstall(sc)
			login := authtest.StartLogin(t, ac)

			if tc.remove {
				if _, err := srv.removeServerFromAgent("svc"); err != nil {
					t.Fatal(err)
				}
			}
			auth.ExchangeCallbackCode(login, "test-auth-code")
			srv.authWg.Add(1)
			srv.runAuthFlow(t.Context(), installAtStartAuth, newAuthFlow(func() {}, login, sc))

			if got := len(srv.reg.AllFull()) > 0; got != tc.wantTools {
				t.Errorf("tools registered = %v, want %v", got, tc.wantTools)
			}
		})
	}
}

func TestRunAuthFlow_loginToADisabledServerSavesTheTokenWithoutConnecting(t *testing.T) {
	echomcp := testutil.Binary(t, "ECHOMCP_BIN")
	srv := newInternalAuthTestServer(t)
	t.Cleanup(srv.Close)
	ac := authtest.NewTokenServer(t).AuthConfig()
	ac.ResourceURL = "https://mcp.example.com/mcp"
	disabled := false
	sc := config.ServerConfig{Name: "svc", Command: echomcp, URL: ac.ResourceURL, Auth: ac, Enabled: &disabled}
	login := authtest.StartLogin(t, ac)
	auth.ExchangeCallbackCode(login, "test-auth-code")

	srv.authWg.Add(1)
	srv.runAuthFlow(t.Context(), srv.replacingInstall(sc), newAuthFlow(func() {}, login, sc))

	if _, err := auth.LoadLogin(srv.configDir, "svc"); err != nil {
		t.Fatalf("saved login: %v; want the token saved", err)
	}
	if len(srv.reg.AllFull()) > 0 {
		t.Error("the login connected a disabled server")
	}
}

func TestRunAuthFlow_CloseCancelsReconnectAfterLogin(t *testing.T) {
	srv := newInternalAuthTestServer(t)
	t.Cleanup(srv.Close)
	upstream := authtest.NewOAuthMCPServer(
		t,
		authtest.OAuthMCPServerParams{AuthServerURL: authtest.NewTokenServer(t).Srv.URL, BlockConnecting: true},
	)
	ctx := startCompletedAuthFlow(t, srv, config.ServerConfig{Name: "svc", Transport: "http", URL: upstream.URL})
	testutil.WaitForReceive(t, upstream.Connecting, "the OAuth reconnect to reach upstream initialize")

	closed := make(chan struct{})
	go func() { srv.Close(); close(closed) }()
	testutil.WaitForReceive(t, closed, "Close to complete while upstream initialize stays blocked")
	if ctx.Err() != context.Canceled {
		t.Fatal("Close did not cancel the auth flow")
	}
	if got := len(srv.reg.AllFull()); got != 0 {
		t.Errorf("registered tools after Close = %d, want 0", got)
	}
	if srv.isUpstreamRegistered("svc") {
		t.Error("server remained registered after Close")
	}
}

func startCompletedAuthFlow(t *testing.T, srv *Server, sc config.ServerConfig) context.Context {
	t.Helper()
	sc.Auth = authtest.NewTokenServer(t).AuthConfig()
	resource, err := auth.CanonicalResourceURI(sc.URL)
	if err != nil {
		t.Fatal(err)
	}
	sc.Auth.ResourceURL = resource
	login := authtest.StartLogin(t, sc.Auth)
	auth.ExchangeCallbackCode(login, "test-auth-code")
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	flow := newAuthFlow(cancel, login, sc)
	srv.storeAuthFlow(sc.Name, flow)
	install := srv.replacingInstall(sc)
	srv.authWg.Add(1)
	go srv.runAuthFlow(ctx, install, flow)
	return ctx
}
