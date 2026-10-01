//go:build test

package server_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

type configToolEnv struct {
	t   *testing.T
	srv *server.Server
	dir string
}

func newConfigToolEnv(t *testing.T) configToolEnv {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DangerousAllowPrivateURLs = true
	dir := t.TempDir()
	return configToolEnv{t: t, srv: newTestServer(t, server.Params{Config: cfg, ConfigDir: dir}), dir: dir}
}

func (e configToolEnv) call(args map[string]any) (string, bool) {
	e.t.Helper()
	resp := serve(e.t, e.srv, callTool("config", args))
	result, _ := resp["result"].(map[string]any)
	return toolResultText(e.t, resp), result["isError"] == true
}

func (e configToolEnv) addServer(sc map[string]any) (string, bool) {
	e.t.Helper()
	return e.call(map[string]any{"action": "add_server", "config": sc})
}

func (e configToolEnv) removeServer(name string) (string, bool) {
	e.t.Helper()
	return e.call(map[string]any{"action": "remove_server", "server": name})
}

func (e configToolEnv) liveProjectionRules(name string) []string {
	e.t.Helper()
	text, _ := e.call(map[string]any{"action": "status"})
	var status struct {
		Projections map[string][]string `json:"projections"`
	}
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		e.t.Fatalf("status %q: %v", text, err)
	}
	return status.Projections[name]
}

func (e configToolEnv) isSaved(name string) bool {
	_, err := os.Stat(filepath.Join(e.dir, "servers", name+".yaml"))
	return err == nil
}

func (e configToolEnv) assertNotSaved(name string) {
	e.t.Helper()
	if e.isSaved(name) {
		e.t.Errorf("servers/%s.yaml exists, want no file", name)
	}
}

func TestConfigAddServer_savesAndConnectsTheServer(t *testing.T) {
	e := newConfigToolEnv(t)
	upstream := newMCPTestServer(t, pingTools)

	text, failed := e.addServer(map[string]any{
		"name": "added", "transport": "http", "url": upstream.URL,
		"headers":          map[string]any{"Authorization": "Bearer stolen"},
		"projections":      map[string]any{"ping": map[string]any{"include_only": []string{"x"}}},
		"enabled":          false,
		"handshakeTimeout": "0",
	})

	if failed {
		t.Fatalf("add_server failed: %s", text)
	}
	if e.srv.ToolCount("added") == 0 {
		t.Error("added server is not connected")
	}
	saved := readServerYAML(t, e.dir, "added")
	if saved.URL != upstream.URL || !saved.IsEnabled() {
		t.Errorf("saved url %q enabled %v, want %q and enabled", saved.URL, saved.IsEnabled(), upstream.URL)
	}
	if saved.HandshakeTimeout != "" {
		t.Errorf("saved handshake_timeout %q, want the default so a hung server can't hold the name", saved.HandshakeTimeout)
	}
	if !saved.AgentAdded {
		t.Error("saved server lacks agent_added, so a restart would trust it like one the user added")
	}
	if len(saved.Headers) != 0 || saved.Auth != nil || len(saved.Projections) != 0 {
		t.Errorf("saved headers %v, auth %+v, projections %v; want the agent's credentials and rules stripped", saved.Headers, saved.Auth, saved.Projections)
	}
	if rules := e.liveProjectionRules("added"); len(rules) != 0 {
		t.Errorf("live projection rules %v, want none from the agent's config", rules)
	}
}

func TestConfigAddServer_runsTheServerAsARestartWould(t *testing.T) {
	e := newConfigToolEnv(t)
	writeReloadFile(t, filepath.Join(e.dir, "servers", "added.proj.yaml"), "ping:\n  include_only: [x]\n")

	if text, failed := e.addServer(map[string]any{"name": "added", "transport": "http", "url": newMCPTestServer(t, pingTools).URL}); failed {
		t.Fatalf("add_server: %s", text)
	}

	if rules := e.liveProjectionRules("added"); !slices.Equal(rules, []string{"ping"}) {
		t.Errorf("live projection rules %v, want the saved projection file's [ping]", rules)
	}
}

func TestConfigAddServer_aConfiguredOrRunningName_isRefusedAndLeftAlone(t *testing.T) {
	t.Run("configured", func(t *testing.T) {
		e := newConfigToolEnv(t)
		writeServerYAML(t, e.dir, "svc", "transport: http\nurl: https://real.example.com/mcp\n")

		text, failed := e.addServer(map[string]any{"name": "svc", "transport": "http", "url": newMCPTestServer(t, pingTools).URL})

		if !failed || !strings.Contains(text, "svc is already configured; remove it with remove_server first") {
			t.Fatalf("add_server = %q, want the already-configured refusal", text)
		}
		if got := readServerYAML(t, e.dir, "svc").URL; got != "https://real.example.com/mcp" {
			t.Errorf("servers/svc.yaml url = %q, the refused add_server rewrote it", got)
		}
	})
	t.Run("running without a config file", func(t *testing.T) {
		e := newConfigToolEnv(t)
		running := fakeConn("real_tool")
		addEdgeConn(t, e.srv, config.ServerConfig{Name: "svc"}, running)
		if err := auth.Save(e.dir, "svc", &oauth2.Token{AccessToken: "running-token"}); err != nil {
			t.Fatal(err)
		}

		text, failed := e.addServer(map[string]any{"name": "svc", "transport": "http", "url": newMCPTestServer(t, pingTools).URL})

		if !failed || !strings.Contains(text, "svc is already running; remove it with remove_server first") {
			t.Fatalf("add_server = %q, want the already-running refusal", text)
		}
		if running.Closed {
			t.Error("refused add_server disconnected the running svc")
		}
		e.assertNotSaved("svc")
		if tok, err := auth.Load(e.dir, "svc"); err != nil || tok == nil {
			t.Errorf("the running server's token is gone (%v, %v)", tok, err)
		}
	})
}

func TestConfigAddServer_connectFails_leavesNoFiles(t *testing.T) {
	e := newConfigToolEnv(t)

	_, failed := e.addServer(map[string]any{"name": "down", "transport": "http", "url": "http://127.0.0.1.github.com:1/mcp"})

	if !failed {
		t.Fatal("add_server to an unreachable URL succeeded")
	}
	if entries, _ := os.ReadDir(filepath.Join(e.dir, "servers")); len(entries) != 0 { //nolint:errcheck // a missing dir is the empty result wanted
		t.Errorf("servers/ holds %v after a failed add, want the server file and its bundled projection gone", entries)
	}
}

func TestConfigAddServer_envReferenceInURL_isRefusedSoARestartCantExpandIt(t *testing.T) {
	e := newConfigToolEnv(t)

	text, failed := e.addServer(map[string]any{"name": "leak", "transport": "http", "url": newMCPTestServer(t, pingTools).URL + "/?t=${HOME}"})

	if !failed || !strings.Contains(text, "isn't expanded") {
		t.Fatalf("add_server = %q, want the unexpanded-reference refusal", text)
	}
	e.assertNotSaved("leak")
}

func TestConfigAddServer_aSavedAgentServerNeverGetsOAuthFromItsOwnChallenge(t *testing.T) {
	e := newConfigToolEnv(t)
	upstream, demandOAuth := newMCPServerThatLaterDemandsOAuth(t)
	if text, failed := e.addServer(map[string]any{"name": "evil", "transport": "http", "url": upstream.URL}); failed {
		t.Fatalf("add_server: %s", text)
	}
	demandOAuth()

	restartErr := restartWithSavedServer(t, e.dir, "evil")

	if errors.Is(restartErr, transport.ErrReauthRequired) || config.IsOAuthDetected(e.dir, "evil") {
		t.Errorf("restart err = %v, OAuth marker = %v; want a plain failure and no marker", restartErr, config.IsOAuthDetected(e.dir, "evil"))
	}
	if text, failed := e.call(map[string]any{"action": "start_auth", "server": "evil"}); !failed {
		t.Errorf("start_auth = %q, want it refused: the server's own metadata would pick where the code goes", text)
	}
}

func newMCPServerThatLaterDemandsOAuth(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	var demanding atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if demanding.Load() {
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fakeMCPHandle(w, r, pingTools)
	}))
	t.Cleanup(srv.Close)
	return srv, func() { demanding.Store(true) }
}

func restartWithSavedServer(t *testing.T, dir, name string) error {
	t.Helper()
	cfg, servers, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DangerousAllowPrivateURLs = true
	restarted := newTestServer(t, server.Params{Config: cfg, ConfigDir: dir})
	t.Cleanup(restarted.Close)
	return restarted.AddUpstream(t.Context(), *config.FindServer(servers, name))
}

func TestConfigAddServer_overlappingAddsOfOneName_theFirstWins(t *testing.T) {
	e := newConfigToolEnv(t)
	slow, inHandshake, finishHandshake := newGatedMCPServer(t)
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		e.addServer(map[string]any{"name": "svc", "transport": "http", "url": slow.URL})
	}()
	<-inHandshake
	secondResult := make(chan string, 1)
	secondDone := make(chan struct{})
	winner := newMCPTestServer(t, pingTools)
	go func() {
		var text string
		defer func() { secondResult <- text; close(secondDone) }()
		text, _ = e.addServer(map[string]any{"name": "svc", "transport": "http", "url": winner.URL})
	}()
	e.waitUntilQueuedOrDone("svc", secondDone)

	finishHandshake()
	<-firstDone

	if text := <-secondResult; !strings.Contains(text, "already") {
		t.Fatalf("overlapping add_server = %q, want it refused", text)
	}
	if e.srv.ToolCount("svc") == 0 {
		t.Error("the first add_server's server is not connected")
	}
	if saved := readServerYAML(t, e.dir, "svc"); saved.URL != slow.URL {
		t.Errorf("saved url = %q, want the first add's %q", saved.URL, slow.URL)
	}
}

func TestConfigRemoveServer_duringAnAddOfTheSameName_leavesSavedEqualToLive(t *testing.T) {
	e := newConfigToolEnv(t)
	slow, inHandshake, finishHandshake := newGatedMCPServer(t)
	addDone := make(chan struct{})
	go func() {
		defer close(addDone)
		e.addServer(map[string]any{"name": "svc", "transport": "http", "url": slow.URL})
	}()
	<-inHandshake
	removeDone := make(chan struct{})
	go func() {
		defer close(removeDone)
		e.removeServer("svc")
	}()
	e.waitUntilQueuedOrDone("svc", removeDone)

	finishHandshake()
	<-addDone
	<-removeDone

	if saved, live := e.isSaved("svc"), e.srv.ToolCount("svc") > 0; saved || live {
		t.Errorf("saved=%v live=%v, want the remove to run after the add and leave neither", saved, live)
	}
}

func TestConfigAddServer_anAddThatSucceedsAfterARemoveStaysSavedAndLive(t *testing.T) {
	e := newConfigToolEnv(t)
	slow, inHandshake, finishHandshake := newGatedMCPServer(t)
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		e.addServer(map[string]any{"name": "svc", "transport": "http", "url": slow.URL})
	}()
	<-inHandshake
	later := newMCPTestServer(t, pingTools)
	removeThenAddDone := make(chan struct{})
	var laterAddFailed bool
	go func() {
		defer close(removeThenAddDone)
		e.removeServer("svc")
		_, laterAddFailed = e.addServer(map[string]any{"name": "svc", "transport": "http", "url": later.URL})
	}()
	e.waitUntilQueuedOrDone("svc", removeThenAddDone)

	finishHandshake()
	<-firstDone
	<-removeThenAddDone

	if saved, live := e.isSaved("svc"), e.srv.ToolCount("svc") > 0; laterAddFailed || !saved || !live {
		t.Errorf("later add failed=%v, saved=%v, live=%v; want the add that reported success kept", laterAddFailed, saved, live)
	}
}

func TestConfigAddAndRemoveServer_racingOnOneName_alwaysLeaveSavedEqualToLive(t *testing.T) {
	upstream := newMCPTestServer(t, pingTools)
	for i := range 30 {
		e := newConfigToolEnv(t)
		added := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			defer close(added)
			e.addServer(map[string]any{"name": "svc", "transport": "http", "url": upstream.URL})
		})
		wg.Go(func() {
			for e.srv.ToolCount("svc") == 0 && !isClosed(added) {
				runtime.Gosched()
			}
			e.removeServer("svc")
		})
		wg.Wait()

		if saved, live := e.isSaved("svc"), e.srv.ToolCount("svc") > 0; saved != live {
			t.Fatalf("iteration %d: saved=%v live=%v after both calls returned", i, saved, live)
		}
	}
}

// done ends the wait too, so a missing lock fails the test instead of hanging it.
func (e configToolEnv) waitUntilQueuedOrDone(name string, done <-chan struct{}) {
	for e.srv.NameLockCallers(name) < 2 && !isClosed(done) {
		runtime.Gosched()
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

func newGatedMCPServer(t *testing.T) (srv *httptest.Server, inHandshake <-chan struct{}, finishHandshake func()) {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce, releaseOnce sync.Once
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(started) })
		<-release
		fakeMCPHandle(w, r, pingTools)
	}))
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(srv.Close)
	t.Cleanup(finish)
	return srv, started, finish
}

func TestConfigRemoveServer(t *testing.T) {
	t.Run("deletes the server file and disconnects", func(t *testing.T) {
		e := newConfigToolEnv(t)
		if text, failed := e.addServer(map[string]any{"name": "svc", "transport": "http", "url": newMCPTestServer(t, pingTools).URL}); failed {
			t.Fatalf("add_server: %s", text)
		}

		if text, failed := e.removeServer("svc"); failed {
			t.Fatalf("remove_server: %s", text)
		}

		e.assertNotSaved("svc")
		if e.srv.ToolCount("svc") != 0 {
			t.Error("svc still connected after remove_server")
		}
	})
	t.Run("disconnects a server with no config file", func(t *testing.T) {
		e := newConfigToolEnv(t)
		addEdgeConn(t, e.srv, config.ServerConfig{Name: "svc"}, fakeConn("ping"))

		if text, failed := e.removeServer("svc"); failed {
			t.Fatalf("remove_server: %s", text)
		}

		if e.srv.ToolCount("svc") != 0 {
			t.Error("svc still connected after remove_server")
		}
	})
}
