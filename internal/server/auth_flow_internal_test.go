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
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/ops"
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
	srv.authFlows["srv1"] = newAuthFlow(func() { cancelled = true }, login, "")
	srv.authMu.Unlock()

	srv.cancelExistingAuthFlow("srv1", errLoginReplaced)

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
	srv.storeAuthFlow("svc", newAuthFlow(cancel, login, ""))

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
	staleFlow := newAuthFlow(staleCancel, staleLogin, "")

	activeLogin := authtest.StartLogin(t, &config.AuthConfig{})

	_, activeCancel := context.WithCancel(context.Background())
	defer activeCancel()
	activeFlow := newAuthFlow(activeCancel, activeLogin, "")

	srv.authMu.Lock()
	srv.authFlows["svc"] = activeFlow
	srv.authMu.Unlock()

	authCtx, cancel := context.WithCancel(context.Background())
	cancel()

	srv.authWg.Add(1)
	srv.runAuthFlow(authCtx, srv.loginTargetFor(config.ServerConfig{Name: "svc"}), staleFlow)

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
			configtest.WriteServer(t, srv.configDir, sc)
			installAtStartAuth := srv.loginTargetFor(sc)
			login := authtest.StartLogin(t, ac)

			if tc.remove {
				if _, err := srv.removeServerFromAgent("svc"); err != nil {
					t.Fatal(err)
				}
			}
			auth.ExchangeCallbackCode(login, "test-auth-code")
			srv.authWg.Add(1)
			srv.runAuthFlow(t.Context(), installAtStartAuth, newAuthFlow(func() {}, login, sc.Name))
			srv.WaitForStartupConnects()

			if got := len(srv.reg.AllFull()) > 0; got != tc.wantTools {
				t.Errorf("tools registered = %v, want %v", got, tc.wantTools)
			}
		})
	}
}

func TestRunAuthFlow_aLoginFinishingAfterItsFileIsDeleted_removesTheServer(t *testing.T) {
	srv := newInternalAuthTestServer(t)
	t.Cleanup(srv.Close)
	ac := authtest.NewTokenServer(t).AuthConfig()
	ac.ResourceURL = "https://mcp.example.com/mcp"
	sc := config.ServerConfig{Name: "svc", URL: "https://mcp.example.com/mcp", Auth: ac}
	configtest.WriteServer(t, srv.configDir, sc)
	srv.recordConfigServers([]config.ServerConfig{sc})
	target := srv.loginTargetFor(sc)
	login := authtest.StartLogin(t, ac)
	if err := os.Remove(config.ServerPath(srv.configDir, "svc")); err != nil {
		t.Fatal(err)
	}

	auth.ExchangeCallbackCode(login, "test-auth-code")
	srv.authWg.Add(1)
	srv.runAuthFlow(t.Context(), target, newAuthFlow(func() {}, login, sc.Name))

	if srv.upstreams.get("svc") != nil {
		t.Error("svc still runs after its login finished, though its file was deleted meanwhile")
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
	configtest.WriteServer(t, srv.configDir, sc)
	login := authtest.StartLogin(t, ac)
	auth.ExchangeCallbackCode(login, "test-auth-code")

	srv.authWg.Add(1)
	srv.runAuthFlow(t.Context(), srv.loginTargetFor(sc), newAuthFlow(func() {}, login, sc.Name))

	if _, err := auth.LoadLogin(srv.configDir, "svc"); err != nil {
		t.Fatalf("saved login: %v; want the token saved", err)
	}
	if len(srv.reg.AllFull()) > 0 {
		t.Error("the login connected a disabled server")
	}
}

func TestApplyEditBeforeLogin_aRunningServerItsFileNowDisables_isStopped(t *testing.T) {
	echomcp := testutil.Binary(t, "ECHOMCP_BIN")
	srv := newInternalAuthTestServer(t)
	t.Cleanup(srv.Close)
	sc := config.ServerConfig{Name: "svc", Command: echomcp}
	srv.ConnectUpstreams([]config.ServerConfig{sc})
	srv.WaitForStartupConnects()
	disabled := false
	edited := sc
	edited.Enabled = &disabled
	edited.Args = []string{"--edited"}
	unlock := srv.serverNames.lock("svc")
	defer unlock()

	srv.applyEditBeforeLogin(edited)

	if srv.upstreams.get("svc") != nil {
		t.Error("a login starting for svc left it running, though its file now disables it")
	}
}

func TestHandleStartAuth_aDisabledServer_isRefusedWithTheRemedy(t *testing.T) {
	srv := newInternalAuthTestServer(t)
	t.Cleanup(srv.Close)
	disabled := false
	configtest.WriteServer(t, srv.configDir, config.ServerConfig{
		Name:    "svc",
		URL:     "https://mcp.example.com/mcp",
		Enabled: &disabled,
		Auth:    &config.AuthConfig{Type: config.AuthTypeOAuth2},
	})

	_, err := srv.handleStartAuth(t.Context(), "svc")

	testutil.ErrorContains(t, err, `log in with "mini auth svc"`)
	if srv.hasAuthFlow("svc") {
		t.Error("a login is waiting for a disabled server, which nothing would start once it finishes")
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
	if srv.isUpstreamConnected("svc") {
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
	configtest.WriteServer(t, srv.configDir, sc)
	login := authtest.StartLogin(t, sc.Auth)
	auth.ExchangeCallbackCode(login, "test-auth-code")
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	flow := newAuthFlow(cancel, login, sc.Name)
	srv.storeAuthFlow(sc.Name, flow)
	install := srv.loginTargetFor(sc)
	srv.authWg.Add(1)
	go srv.runAuthFlow(ctx, install, flow)
	return ctx
}

func TestAuthFlow_loginInstallingAnUnrecordedServerRecordsItSoRmRemovesIt(t *testing.T) {
	f := newLoginFixture(t, authtest.OAuthMCPServerParams{})
	stream := f.postLogin(t.Context(), t, "svc")
	authtest.CompleteAuthorization(t, stream.authURL(t), "test-auth-code")
	if line := stream.next(t); line["ok"] != true || !f.srv.isUpstreamConnected("svc") {
		t.Fatalf("outcome = %v, want svc connected by the login", line)
	}

	if err := ops.RemoveServer(f.srv.configDir, "svc"); err != nil {
		t.Fatal(err)
	}
	f.srv.applyServerFiles()

	if f.srv.isUpstreamConnected("svc") {
		t.Error("svc still runs after its file was removed")
	}
}
