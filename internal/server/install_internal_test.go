//go:build test

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/auth/provider"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/transport"
	"golang.org/x/oauth2"
)

func newInstallTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := New(Params{Config: cfg, ConfigDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	t.Cleanup(srv.Close)
	return srv
}

func TestRemoveConfigServer_keepsANameSavedAgainSinceTheServerSetWasLoaded(t *testing.T) {
	srv := newInstallTestServer(t)
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, &transport.FakeConnection{}); err != nil {
		t.Fatal(err)
	}
	srv.recordConfigServers([]config.ServerConfig{{Name: "svc"}})
	path := filepath.Join(srv.configDir, "servers", "svc.yaml")
	configtest.WriteServer(t, srv.configDir, config.ServerConfig{Name: "svc", Command: "run"})

	if srv.removeConfigServer("svc") {
		t.Error("removeConfigServer removed svc while its file is saved and enabled")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !srv.removeConfigServer("svc") {
		t.Error("removeConfigServer kept svc after its file was deleted")
	}
}

func TestInstallChecked_readsLiveAliasesWhileSetProjectionWritesThem(t *testing.T) {
	srv := newInstallTestServer(t)
	srv.replaceProjections(map[string]map[string]*config.ProjectionConfig{"svc": {"getData": {Alias: "fetch"}}}, config.Servers{})
	tools := []transport.ToolDefinition{{Name: "getData", InputSchema: json.RawMessage(`{}`)}}
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 100 {
			srv.storeServerProjection("svc", fmt.Sprintf("tool%d", i), &config.ProjectionConfig{})
		}
	})
	wg.Go(func() {
		for range 20 {
			if err := srv.installChecked(&transport.FakeConnection{}, tools, srv.replacingInstall(config.ServerConfig{Name: "svc"})); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Wait()
}

func TestInstallChecked_guardRejection_closesConn(t *testing.T) {
	srv := newInstallTestServer(t)

	srv.serverOpMu.Lock()
	srv.removeGen["svc"]++
	srv.serverOpMu.Unlock()

	fake := &transport.FakeConnection{}
	in := upstreamInstall{cfg: config.ServerConfig{Name: "svc"}, removeGen: 0}

	err := srv.installChecked(fake, nil, in)

	if !errors.Is(err, errServerRemoved) {
		t.Errorf("expected errServerRemoved, got %v", err)
	}
	if !fake.Closed {
		t.Error("expected connection to be closed on guard rejection")
	}
}

func TestInstallChecked_namesToolsByTheLiveProjections(t *testing.T) {
	tools := []transport.ToolDefinition{{Name: "getData", InputSchema: json.RawMessage(`{}`)}}
	aliased := map[string]*config.ProjectionConfig{"getData": {Alias: "fetch"}}

	t.Run("a new server takes its config's projections", func(t *testing.T) {
		srv := newInstallTestServer(t)

		in := srv.newServerInstall(config.ServerConfig{Name: "svc", Projections: aliased})
		if err := srv.installChecked(&transport.FakeConnection{}, tools, in); err != nil {
			t.Fatal(err)
		}

		if got := registeredToolNames(srv); !slices.Equal(got, []string{"svc.fetch"}) {
			t.Errorf("tools = %v, want [svc.fetch]", got)
		}
	})
	t.Run("a reinstall whose config's projections failed to load keeps the live ones", func(t *testing.T) {
		srv := newInstallTestServer(t)
		srv.replaceProjections(map[string]map[string]*config.ProjectionConfig{"svc": aliased}, config.Servers{})
		reloaded := config.ServerConfig{Name: "svc", ProjectionsErr: &config.SourceError{ServerName: "svc", Err: errors.New("parse failed")}}

		if err := srv.installChecked(&transport.FakeConnection{}, tools, srv.replacingInstall(reloaded)); err != nil {
			t.Fatal(err)
		}

		if got := registeredToolNames(srv); !slices.Equal(got, []string{"svc.fetch"}) {
			t.Errorf("tools = %v, want [svc.fetch]: a reinstall, like finishing an OAuth login, must not drop the kept alias", got)
		}
	})
}

func TestAddServerFromAgent_aFailedAddStopsAnInstallStartedMeanwhile(t *testing.T) {
	srv := newInstallTestServer(t)
	srv.cfg.DangerousAllowPrivateURLs = true
	startedDuringTheAdd := srv.replacingInstall(config.ServerConfig{Name: "svc"})

	if _, err := srv.addServerFromAgent(t.Context(), &config.ServerConfig{Name: "svc", Transport: "http", URL: "http://127.0.0.1:1/mcp"}); err == nil {
		t.Fatal("add_server to an unreachable URL succeeded")
	}

	if err := srv.installChecked(&transport.FakeConnection{}, nil, startedDuringTheAdd); !errors.Is(err, errServerRemoved) {
		t.Errorf("install started during the failed add = %v, want errServerRemoved so no unsaved server runs", err)
	}
}

func TestAddServerFromAgent_stopsAnInstallStartedForTheNamesEarlierServer(t *testing.T) {
	echomcp := os.Getenv("ECHOMCP_BIN")
	if echomcp == "" {
		t.Fatal("ECHOMCP_BIN not set; run check.sh or: go build -o /tmp/echomcp ./cmd/echomcp && ECHOMCP_BIN=/tmp/echomcp go test ...")
	}
	srv := newInstallTestServer(t)
	srv.cfg.DangerousAllowRuntimeStdio = true
	startedForTheEarlierServer := srv.replacingInstall(config.ServerConfig{Name: "svc", Command: "earlier"})

	if _, err := srv.addServerFromAgent(t.Context(), &config.ServerConfig{Name: "svc", Command: echomcp}); err != nil {
		t.Fatal(err)
	}

	if err := srv.installChecked(&transport.FakeConnection{}, nil, startedForTheEarlierServer); !errors.Is(err, errServerRemoved) {
		t.Errorf("install for the earlier svc = %v, want errServerRemoved so it can't replace the added one", err)
	}
}

func TestRemoveServerFromAgent_anAddOfTheNameWaitsUntilTheRemoveFinishes(t *testing.T) {
	srv := newInstallTestServer(t)
	srv.cfg.DangerousAllowPrivateURLs = true
	configtest.WriteServer(t, srv.configDir, config.ServerConfig{
		Name:      "svc",
		Transport: "http",
		URL:       "http://127.0.0.1:1/mcp",
	})
	srv.authMu.Lock() // pauses the remove: detaching the server starts by taking authMu
	resume := sync.OnceFunc(srv.authMu.Unlock)
	t.Cleanup(resume) // closing the server needs authMu, even when the test stops early
	removed := make(chan struct{})
	go func() { defer close(removed); _, _ = srv.removeServerFromAgent("svc") }() // the outcome checked is the add's
	waitUntil(t, "the remove holds svc", func() bool { return srv.NameLockCallers("svc") == 1 })

	added := make(chan struct{})
	go func() {
		defer close(added)
		_, _ = srv.addServerFromAgent(context.Background(), &config.ServerConfig{
			Name:      "svc",
			Transport: "http",
			URL:       "http://127.0.0.1:1/mcp",
		}) // fails to connect either way
	}()
	waitUntil(t, "the add waits for svc or finishes", func() bool { return srv.NameLockCallers("svc") == 2 || isClosed(added) })
	addFinishedMidRemove := isClosed(added)
	resume()
	<-removed
	<-added

	if addFinishedMidRemove {
		t.Error("add_server of svc ran while remove_server of svc was still in progress")
	}
}

func TestStartAuth_waitsUntilAnAddOrRemoveOfTheNameFinishes(t *testing.T) {
	srv := newInstallTestServer(t)
	unlock := sync.OnceFunc(srv.serverNames.lock("svc"))
	t.Cleanup(unlock)

	started := make(chan struct{})
	go func() { defer close(started); _, _ = srv.handleStartAuth("svc") }() // svc isn't saved, so it fails either way
	waitUntil(t, "start_auth waits for svc or finishes", func() bool { return srv.NameLockCallers("svc") == 2 || isClosed(started) })
	startedMidChange := isClosed(started)
	unlock()
	<-started

	if startedMidChange {
		t.Error("start_auth for svc ran while an add_server or remove_server of svc held it, so it could write files the remove deletes")
	}
}

func TestChangeSavedServer_keepsSetProjectionOutUntilTheChangeFinishes(t *testing.T) {
	srv := newInstallTestServer(t)

	_ = srv.changeSavedServer("svc", func() error {
		if srv.persistMu.TryLock() {
			srv.persistMu.Unlock()
			t.Error("set_projection could write svc.proj.yaml during the change, recreating it after a remove")
		}
		return nil
	})
}

func TestAddServerFromAgent_aTokenRefreshForTheNamesEarlierServerLeavesNoToken(t *testing.T) {
	echomcp := os.Getenv("ECHOMCP_BIN")
	if echomcp == "" {
		t.Fatal("ECHOMCP_BIN not set; run check.sh or: go build -o /tmp/echomcp ./cmd/echomcp && ECHOMCP_BIN=/tmp/echomcp go test ...")
	}
	srv := newInstallTestServer(t)
	srv.cfg.DangerousAllowRuntimeStdio = true
	finishRefresh, refreshed := startTokenRefresh(t, srv)

	srv.authMu.Lock() // pauses the add: detaching the name starts by taking authMu
	resume := sync.OnceFunc(srv.authMu.Unlock)
	t.Cleanup(resume) // closing the server needs authMu, even when the test stops early
	added := make(chan error, 1)
	go func() {
		_, err := srv.addServerFromAgent(context.Background(), &config.ServerConfig{Name: "svc", Command: echomcp})
		added <- err
	}()
	waitUntil(t, "the add holds svc", func() bool { return srv.NameLockCallers("svc") == 1 })
	finishRefresh()
	waitForChannel(t, "the refresh saves its token", refreshed)
	resume()

	if err := <-added; err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Load(srv.configDir, "svc"); !auth.IsNotFound(err) {
		t.Errorf("token after add_server: %v, want none: the earlier svc's refresh saved it after the add cleared it", err)
	}
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		runtime.Gosched()
	}
}

func TestRetryStartupAfter_stopsForFailuresARetryCantFix(t *testing.T) {
	srv := newInstallTestServer(t)
	refused := fmt.Errorf("connect to svc: svc: %w", invoke.ErrAgentCommandNotAllowed)

	if srv.retryStartupAfter("svc", refused, time.Second) {
		t.Error("startup retries a command dangerous_allow_runtime_stdio doesn't allow, so it warns forever")
	}
	unset := fmt.Errorf("connect to svc: %w", &config.UnsetEnvError{Field: "headers.Authorization", Names: []string{"GITHUB_TOKEN"}})
	if srv.retryStartupAfter("svc", unset, time.Second) {
		t.Error("startup retries a server whose environment variable isn't set, which a retry can't fix")
	}
	if !srv.retryStartupAfter("svc", errors.New("connection refused"), time.Second) {
		t.Error("startup gave up on an ordinary connect failure")
	}
}

func TestRollBackAdd_reportsAServerItCouldNotRemove(t *testing.T) {
	srv := newInstallTestServer(t)

	if err := srv.rollBackAdd("svc"); err == nil || !strings.Contains(err.Error(), "remove it with remove_server") {
		t.Errorf("rollBackAdd = %v, want the agent told the server is still saved", err)
	}
}

func TestRemoveServerFromAgent_aTokenRefreshFinishingMidRemoveLeavesNoToken(t *testing.T) {
	srv := newInstallTestServer(t)
	configtest.WriteServer(t, srv.configDir, config.ServerConfig{
		Name:      "svc",
		Transport: "http",
		URL:       "http://127.0.0.1:1/mcp",
	})
	finishRefresh, refreshed := startTokenRefresh(t, srv)

	srv.authMu.Lock() // pauses the remove: detaching the server starts by taking authMu
	resume := sync.OnceFunc(srv.authMu.Unlock)
	t.Cleanup(resume) // closing the server needs authMu, even when the test stops early
	removed := make(chan struct{})
	go func() { defer close(removed); _, _ = srv.removeServerFromAgent("svc") }()
	waitUntil(t, "the remove holds svc", func() bool { return srv.NameLockCallers("svc") == 1 })
	finishRefresh()
	waitForChannel(t, "the refresh saves its token", refreshed)
	resume()
	<-removed

	if _, err := auth.Load(srv.configDir, "svc"); !auth.IsNotFound(err) {
		t.Errorf("token after remove_server: %v, want none: a refresh that finished mid-remove saved it back", err)
	}
}

// startTokenRefresh starts refreshing svc's expired token and returns once the refresh is waiting
// on the token endpoint; finish lets it save the new token, and refreshed closes when it has.
func startTokenRefresh(t *testing.T, srv *Server) (finish func(), refreshed <-chan struct{}) {
	t.Helper()
	epoch := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir:  srv.configDir,
		ServerName: "svc",
		Token: &oauth2.Token{
			AccessToken:  "old",
			RefreshToken: "r",
			Expiry:       epoch.Add(-time.Second),
		},
	})
	endpoint, refreshReached, finish := gatedTokenEndpoint(t)
	login, err := srv.providerRegistry.GetOrCreate(provider.Params{
		AuthConfig: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "c", TokenURL: endpoint + "/token"},
		ConfigDir:  srv.configDir,
		ServerName: "svc",
		ServerURL:  "http://127.0.0.1:1/mcp",
		Clock:      clock.NewFakeAt(epoch),
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = login.Authorization(context.Background()) }() // the refresh's outcome is read from disk
	waitForChannel(t, "the token refresh reaches the endpoint", refreshReached)
	return finish, done
}

func TestCommitTokenUnlessRemoved(t *testing.T) {
	login := func(srv *Server) upstreamInstall {
		sc := config.ServerConfig{Name: "svc", Transport: "http", URL: "http://127.0.0.1:1/mcp",
			Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "c", TokenURL: "http://127.0.0.1:1/token"}}
		return upstreamInstall{cfg: sc, removeGen: srv.snapshotRemoveGen("svc")}
	}

	t.Run("saves the token of a login whose server is still there", func(t *testing.T) {
		srv := newInstallTestServer(t)
		if err := srv.commitTokenUnlessRemoved(login(srv), &oauth2.Token{AccessToken: "fresh"}); err != nil {
			t.Fatal(err)
		}
		if tok, err := auth.Load(srv.configDir, "svc"); err != nil || tok.AccessToken != "fresh" {
			t.Errorf("saved token = %v, %v; want fresh", tok, err)
		}
	})
	t.Run("refuses a login that finishes after its server was removed", func(t *testing.T) {
		srv := newInstallTestServer(t)
		startedBeforeTheRemove := login(srv)
		srv.detachAndCloseServer("svc")

		err := srv.commitTokenUnlessRemoved(startedBeforeTheRemove, &oauth2.Token{AccessToken: "late"})

		if !errors.Is(err, errServerRemoved) {
			t.Errorf("commit = %v, want errServerRemoved", err)
		}
		if _, err := auth.Load(srv.configDir, "svc"); !auth.IsNotFound(err) {
			t.Errorf("token after a refused commit: %v, want none", err)
		}
	})
}

func gatedTokenEndpoint(t *testing.T) (url string, reached <-chan struct{}, finish func()) {
	t.Helper()
	endpoint := authtest.NewTokenServer(t)
	ready, gate := make(chan struct{}), make(chan struct{})
	endpoint.Mu.Lock()
	endpoint.HoldReady, endpoint.HoldGate = ready, gate
	endpoint.Mu.Unlock()
	finish = sync.OnceFunc(func() { close(gate) })
	t.Cleanup(finish)
	return endpoint.Srv.URL, ready, finish
}

func waitForChannel(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting until %s", what)
	}
}
