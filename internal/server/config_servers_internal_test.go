//go:build test

package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

func TestApplyServerFile_skipsANameAnotherOperationHolds(t *testing.T) {
	srv := newInstallTestServer(t)
	if err := srv.AddConnection(
		t.Context(),
		config.ServerConfig{Name: "svc"},
		&transport.FakeConnection{},
	); err != nil {
		t.Fatal(err)
	}
	srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})
	unlock := srv.serverNames.lock("svc")

	removedWhileHeld := srv.applyServerFile(t.Context(), "svc")
	unlock()

	if removedWhileHeld {
		t.Error("applyServerFile removed svc while another operation held its name")
	}
	if !srv.applyServerFile(t.Context(), "svc") {
		t.Error("applyServerFile kept svc once its name was free, want the next pass to remove it")
	}
}

func TestApplyServerFile_reconnectsAServerStoppedAtNeedsAuthOnceItsLoginIsSaved(t *testing.T) {
	srv, sc := newNeedsAuthServer(t)
	if srv.applyServerFile(t.Context(), "svc"); srv.ToolCount("svc") != 0 || !srv.isConfigServer("svc") {
		t.Fatal("svc was touched before any login was saved")
	}

	writeLogin(t, srv)
	srv.applyServerFile(t.Context(), "svc")
	srv.connector.wait()

	if srv.ToolCount("svc") == 0 {
		t.Error("svc still has no tools after its login was saved, want it reconnected")
	}
	if srv.liveProjections("svc") == nil {
		t.Errorf("svc lost its projections %v when it reconnected", sc.Projections)
	}
}

func TestApplyServerFile_aLoginSavedWhileTheFileIsHalfWrittenAppliesOnceItIsComplete(t *testing.T) {
	srv, sc := newNeedsAuthServer(t)
	writeLogin(t, srv)
	testutil.WriteFile(t, config.ServerPath(srv.configDir, "svc"), "")

	srv.applyServerFile(t.Context(), "svc")
	configtest.WriteServer(t, srv.configDir, sc)
	srv.applyServerFile(t.Context(), "svc")

	if !connectsWithin(srv, 3*time.Second) || srv.ToolCount("svc") == 0 {
		t.Error("svc never connected: the half-written file took the place of its complete settings")
	}
}

func TestApplyServerFile_leavesAServerAtNeedsAuthWithoutANewLogin(t *testing.T) {
	cases := map[string]func(t *testing.T) *Server{
		"login saved before the attempt": func(t *testing.T) *Server {
			srv := newInstallTestServer(t)
			writeLogin(t, srv)
			recordNeedsAuth(t, srv)
			return srv
		},
		"login in flight": func(t *testing.T) *Server {
			srv, _ := newNeedsAuthServer(t)
			srv.storeAuthFlow("svc", &authFlowState{cancel: func() {}})
			writeLogin(t, srv)
			return srv
		},
	}
	for name, setUp := range cases {
		t.Run(name, func(t *testing.T) {
			srv := setUp(t)

			srv.applyServerFile(t.Context(), "svc")
			srv.connector.wait()

			if srv.ToolCount("svc") != 0 {
				t.Error("svc reconnected without a login saved since its attempt")
			}
		})
	}
}

// connectsWithin reports whether every connect attempt finished in time; one retrying settings that
// can't connect never does.
func connectsWithin(srv *Server, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		srv.connector.wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func newNeedsAuthServer(t *testing.T) (*Server, config.ServerConfig) {
	t.Helper()
	srv := newInstallTestServer(t)
	return srv, recordNeedsAuth(t, srv)
}

func recordNeedsAuth(t *testing.T, srv *Server) config.ServerConfig {
	t.Helper()
	sc := config.ServerConfig{
		Name:        "svc",
		Command:     testutil.Binary(t, "ECHOMCP_BIN"),
		Projections: map[string]*config.ProjectionConfig{"*": {IncludeOnly: []string{"a"}}},
	}
	configtest.WriteServer(t, srv.configDir, sc)
	srv.replaceProjections(map[string]map[string]*config.ProjectionConfig{"svc": sc.Projections}, config.Servers{})
	srv.recordConfigServers([]config.ServerConfig{sc})
	srv.recordStartupFailure(srv.startupInstall(sc), needsAuthStatus{})
	return sc
}

func writeLogin(t *testing.T, srv *Server) {
	t.Helper()
	testutil.WriteFile(t, auth.TokenPath(srv.configDir, "svc"), "{}")
}

func TestApplyServerFile_anEditCancelsALoginStartedForTheOldSettings(t *testing.T) {
	srv, sc := newNeedsAuthServer(t)
	cancelled := false
	login := authtest.StartLogin(t, &config.AuthConfig{
		Type:     config.AuthTypeOAuth2,
		ClientID: "c",
		AuthURL:  "https://auth.example.com/authorize",
		TokenURL: "https://auth.example.com/token",
	})
	srv.storeAuthFlow("svc", &authFlowState{cancel: func() { cancelled = true }, login: login})

	sc.Args = []string{"--moved"}
	configtest.WriteServer(t, srv.configDir, sc)
	srv.applyServerFile(t.Context(), "svc")
	srv.connector.wait()

	if !cancelled || srv.hasAuthFlow("svc") {
		t.Error("a login for svc's old settings is still waiting after svc was edited")
	}
}

func TestSettingsChanged_OAuthMiniDetectedFromA401IsNotAnEdit(t *testing.T) {
	srv := newInstallTestServer(t)
	saved := config.ServerConfig{Name: "svc", Transport: "http", URL: "https://mcp.example.com/mcp"}
	configtest.WriteServer(t, srv.configDir, saved)
	srv.recordConfigServers([]config.ServerConfig{saved})

	if err := config.MarkOAuthDetected(srv.configDir, "svc"); err != nil {
		t.Fatal(err)
	}
	srv.recordStartupFailure(srv.startupInstall(saved), needsAuthStatus{})

	loaded, _, err := config.LookupServer(srv.configDir, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Auth == nil {
		t.Fatal("the OAuth mini detected isn't merged into the loaded config, so this test proves nothing")
	}
	if srv.settingsChanged(loaded) {
		t.Error(
			"the OAuth mini detected after a 401 reads as an edit, so the next check reconnects svc and cancels its login",
		)
	}
}

func TestSettingsChanged_aLoginFillingInTheRecordedAuthIsNotAnEdit(t *testing.T) {
	srv := newInstallTestServer(t)
	saved := config.ServerConfig{
		Name: "svc",
		URL:  "https://mcp.example.com",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2},
	}
	running := saved
	srv.recordConfigServers([]config.ServerConfig{running})

	running.Auth.ResourceURL = "https://mcp.example.com"
	running.Auth.TokenURL = "https://auth.example.com/token"

	fresh := saved
	fresh.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2}
	if srv.settingsChanged(fresh) {
		t.Error("endpoints a login wrote into the running config read as an edit of the file")
	}
}

func TestSameSettings(t *testing.T) {
	disabled := false
	base := config.ServerConfig{Name: "svc", URL: "https://mcp.example.com"}
	same := map[string]func(*config.ServerConfig){
		"projections": func(sc *config.ServerConfig) {
			sc.Projections = map[string]*config.ProjectionConfig{"*": {IncludeOnly: []string{"a"}}}
		},
		"broken projections": func(sc *config.ServerConfig) { sc.ProjectionsErr = &config.SourceError{} },
		"unset env":          func(sc *config.ServerConfig) { sc.UnsetEnv = errors.New("unset") },
		"enabled":            func(sc *config.ServerConfig) { sc.Enabled = &disabled },
	}
	for name, edit := range same {
		edited := base
		edit(&edited)
		if !sameSettings(base, edited) {
			t.Errorf("%s: want no reconnect for a change that applies without one", name)
		}
	}
	trusted := base
	trusted.AgentAdded = true
	if sameSettings(base, trusted) {
		t.Error("agent_added: want a reconnect, so trusting a server starts it")
	}
}

func TestClose_stopsTheConfigReloadLoop(t *testing.T) {
	srv := newInstallTestServer(t)
	fc := clock.NewFake()
	srv.clock = fc
	srv.StartConfigReload(context.Background())
	if err := fc.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatal("the reload loop never started its ticker:", err)
	}

	srv.Close()

	stopped, cancel := context.WithCancel(t.Context())
	cancel()
	if fc.BlockUntilContext(stopped, 1) == nil {
		t.Error("the reload loop's ticker is still running after Close")
	}
}

func TestNameLocks_aFailedTryLockLeavesNoReference(t *testing.T) {
	var locks nameLocks
	unlock := locks.lock("svc")

	if _, ok := locks.tryLock("svc"); ok {
		t.Fatal("tryLock took a name another caller holds")
	}
	unlock()

	if len(locks.locks) != 0 {
		t.Errorf("locks = %v after every caller left, want none", locks.locks)
	}
	unlock, ok := locks.tryLock("svc")
	if !ok {
		t.Fatal("tryLock failed on a free name")
	}
	unlock()
}

func TestBackgroundLoop_stopCancelsTheLoopAndWaitsForIt(t *testing.T) {
	var loop backgroundLoop
	exited := false
	loop.start(context.Background(), func(ctx context.Context) {
		<-ctx.Done()
		exited = true
	})

	stopped := make(chan struct{})
	go func() {
		loop.stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("stop didn't end a loop that runs until its context is canceled")
	}
	if !exited {
		t.Error("stop returned before the loop exited")
	}
}

func TestBackgroundLoop_startAfterStopDoesNotRun(t *testing.T) {
	var loop backgroundLoop
	loop.stop()

	ran := false
	loop.start(context.Background(), func(context.Context) { ran = true })
	loop.stop()

	if ran {
		t.Error("a loop started after stop ran, so it would outlive Close")
	}
}
