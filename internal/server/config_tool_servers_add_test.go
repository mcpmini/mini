//go:build test

package server_test

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/mcptest"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
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
	return newConfigToolEnvWithConfig(t, cfg)
}

func newConfigToolEnvWithConfig(t *testing.T, cfg *config.Config) configToolEnv {
	t.Helper()
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
	upstream := mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools})

	text, failed := e.addServer(map[string]any{
		"name": "added", "transport": "http", "url": upstream.URL,
		"headers":           map[string]any{"Authorization": "Bearer stolen"},
		"projections":       map[string]any{"ping": map[string]any{"include_only": []string{"x"}}},
		"permissions":       map[string]any{},
		"enabled":           false,
		"handshakeTimeout":  "0",
		"toolTimeout":       "0",
		"httpClientTimeout": "0",
	})

	if failed {
		t.Fatalf("add_server failed: %s", text)
	}
	if e.srv.ToolCount("added") == 0 {
		t.Error("added server is not connected")
	}
	want := config.ServerConfig{Transport: "http", URL: upstream.URL, AgentAdded: true}
	if saved := readServerYAML(t, e.dir, "added"); !reflect.DeepEqual(saved, want) {
		t.Errorf(
			"saved %+v\nwant only the connection and agent_added: credentials, rules, permissions and timeouts keep mini's defaults",
			saved,
		)
	}
	if rules := e.liveProjectionRules("added"); len(rules) != 0 {
		t.Errorf("live projection rules %v, want none from the agent's config", rules)
	}
}

func TestConfigAddServer_runsTheServerAsARestartWould(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DangerousAllowRuntimeStdio = true
	e := newConfigToolEnvWithConfig(t, cfg)
	githubCommand := filepath.Join(
		t.TempDir(),
		"server-github",
	) // a GitHub command, so the add installs GitHub's bundled projection
	if err := os.Symlink(testutil.Binary(t, "ECHOMCP_BIN"), githubCommand); err != nil {
		t.Fatal(err)
	}

	if text, failed := e.addServer(map[string]any{"name": "added", "command": githubCommand}); failed {
		t.Fatalf("add_server: %s", text)
	}

	saved := readProjectionRuleNames(t, config.ServerPath(e.dir, "added"))
	if rules := slices.Sorted(
		slices.Values(e.liveProjectionRules("added")),
	); len(saved) == 0 ||
		!slices.Equal(rules, saved) {
		t.Errorf("live projection rules %v, want the saved server file's %v", rules, saved)
	}
}

func readProjectionRuleNames(t *testing.T, path string) []string {
	t.Helper()
	data := testutil.ReadFile(t, path)
	var serverFile map[string]any
	if err := yaml.Unmarshal(data, &serverFile); err != nil {
		t.Fatal(err)
	}
	projections, ok := serverFile["projections"].(map[string]any)
	if !ok {
		return nil
	}
	return slices.Sorted(maps.Keys(projections))
}

func TestConfigAddServer_aConfiguredOrRunningName_isRefusedAndLeftAlone(t *testing.T) {
	t.Run("configured", func(t *testing.T) {
		e := newConfigToolEnv(t)
		configtest.WriteServer(t, e.dir, config.ServerConfig{
			Name:      "svc",
			Transport: "http",
			URL:       "https://real.example.com/mcp",
		})

		text, failed := e.addServer(
			map[string]any{
				"name":      "svc",
				"transport": "http",
				"url":       mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools}).URL,
			},
		)

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
		if err := e.srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, running); err != nil {
			t.Fatal(err)
		}
		authtest.SaveToken(t, authtest.TokenFile{
			ConfigDir:  e.dir,
			ServerName: "svc",
			Server:     config.ServerConfig{URL: "https://svc.example.com/mcp"},
			Token:      &oauth2.Token{AccessToken: "running-token"},
		})

		text, failed := e.addServer(
			map[string]any{
				"name":      "svc",
				"transport": "http",
				"url":       mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools}).URL,
			},
		)

		if !failed || !strings.Contains(text, "svc is already running; remove it with remove_server first") {
			t.Fatalf("add_server = %q, want the already-running refusal", text)
		}
		if running.Closed {
			t.Error("refused add_server disconnected the running svc")
		}
		e.assertNotSaved("svc")
		if login, err := auth.LoadLogin(e.dir, "svc"); err != nil || login == nil {
			t.Errorf("the running server's token is gone (%v, %v)", login, err)
		}
	})
}

func TestConfigAddServer_connectFails_leavesNoFiles(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DangerousAllowRuntimeStdio = true
	e := newConfigToolEnvWithConfig(t, cfg)

	_, failed := e.addServer(map[string]any{"name": "down", "command": filepath.Join(t.TempDir(), "server-github")})

	if !failed {
		t.Fatal("add_server of a command that doesn't exist succeeded")
	}
	if entries, _ := os.ReadDir(
		filepath.Join(e.dir, "servers"),
	); len(
		entries,
	) != 0 { //nolint:errcheck // a missing dir is the empty result wanted
		t.Errorf("servers/ holds %v after a failed add, want the server file and its bundled projection gone", entries)
	}
}

func TestConfigAddServer_envReferenceInURL_isRefusedSoARestartCantExpandIt(t *testing.T) {
	e := newConfigToolEnv(t)

	text, failed := e.addServer(
		map[string]any{
			"name":      "leak",
			"transport": "http",
			"url":       mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools}).URL + "/?t=${HOME}",
		},
	)

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
		t.Errorf(
			"restart err = %v, OAuth marker = %v; want a plain failure and no marker",
			restartErr,
			config.IsOAuthDetected(e.dir, "evil"),
		)
	}
	if text, failed := e.call(map[string]any{"action": "start_auth", "server": "evil"}); !failed {
		t.Errorf("start_auth = %q, want it refused: the server's own metadata would pick where the code goes", text)
	}
}

func newMCPServerThatLaterDemandsOAuth(t *testing.T) (*mcptest.Server, func()) {
	t.Helper()
	var demanding atomic.Bool
	srv := mcptest.NewHTTP(t, mcptest.Upstream{
		Tools:     pingTools,
		Authorize: func(mcptest.Request) bool { return !demanding.Load() },
	})
	return srv, func() { demanding.Store(true) }
}

func restartWithSavedServer(t *testing.T, dir, name string) error {
	t.Helper()
	cfg, err := config.LoadMain(dir)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := config.LoadServer(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DangerousAllowPrivateURLs = true
	restarted := newTestServer(t, server.Params{Config: cfg, ConfigDir: dir})
	t.Cleanup(restarted.Close)
	return restarted.AddUpstream(t.Context(), sc)
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
	winner := mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools})
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

func TestConfigAddServer_anAddThatSucceedsAfterARemoveStaysSavedAndLive(t *testing.T) {
	e := newConfigToolEnv(t)
	slow, inHandshake, finishHandshake := newGatedMCPServer(t)
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		e.addServer(map[string]any{"name": "svc", "transport": "http", "url": slow.URL})
	}()
	<-inHandshake
	later := mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools})
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
		t.Errorf(
			"later add failed=%v, saved=%v, live=%v; want the add that reported success kept",
			laterAddFailed,
			saved,
			live,
		)
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

func newGatedMCPServer(t *testing.T) (srv *mcptest.Server, inHandshake <-chan struct{}, finishHandshake func()) {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce, releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(finish)
	srv = mcptest.NewHTTP(t, mcptest.Upstream{
		Tools: pingTools,
		Intercept: func(http.ResponseWriter, *http.Request) bool {
			startOnce.Do(func() { close(started) })
			<-release
			return false
		},
	})
	return srv, started, finish
}

func userOAuthServer(name, url string) config.ServerConfig {
	return config.ServerConfig{
		Name:      name,
		Transport: "http",
		URL:       url,
		Auth: &config.AuthConfig{
			Type:     "oauth2",
			ClientID: "test-client",
			AuthURL:  "http://auth.example/authorize",
			TokenURL: "http://auth.example/token",
		},
	}
}

func (e configToolEnv) connectUserOAuthServer(name, url string) error {
	e.t.Helper()
	configtest.WriteServer(e.t, e.dir, userOAuthServer(name, url))
	sc, err := config.LoadServer(e.dir, name)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.srv.AddUpstream(e.t.Context(), sc)
}

func TestConfigureAddServer_noConfig(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("config", map[string]any{"action": "add_server"}))
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true when config omitted, got: %v", result)
	}
}

func TestConfigureAddServer_viaHTTP(t *testing.T) {
	mcp := mcptest.NewHTTP(t, mcptest.Upstream{Tools: pingTools})
	srv := newServerAllowPrivate(t)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "add_server",
		"config": map[string]any{"name": "dynamic", "transport": "http", "url": mcp.URL},
	}))
	var result map[string]any
	json.Unmarshal([]byte(toolResultText(t, resp)), &result)
	if result["error"] != nil || result["server"] != "dynamic" {
		t.Errorf("expected ok add_server, got: %v", result)
	}

	listText := toolListJSON(t, serve(t, srv, rpc("tools/list", nil)))
	var tools []any
	json.Unmarshal([]byte(listText), &tools)
	if len(tools) != 3 {
		t.Errorf("expected native tool and two builtins after add_server, got %d: %s", len(tools), listText)
	}
}

func newServerAllowPrivate(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DangerousAllowPrivateURLs = true
	return newTestServer(t, server.Params{Config: cfg})
}
