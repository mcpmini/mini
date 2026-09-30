//go:build test

package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
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

func (e configToolEnv) serverFile(name string) string {
	return filepath.Join(e.dir, "servers", name+".yaml")
}

func (e configToolEnv) assertNoServerFile(name string) {
	e.t.Helper()
	if _, err := os.Stat(e.serverFile(name)); err == nil {
		e.t.Errorf("servers/%s.yaml exists, want no file", name)
	}
}

func TestConfigAddServer_savesTheServerToConfig(t *testing.T) {
	e := newConfigToolEnv(t)
	upstream := newMCPTestServer(t, pingTools)

	text, failed := e.addServer(map[string]any{
		"name": "added", "transport": "http", "url": upstream.URL,
		"headers": map[string]any{"Authorization": "Bearer ${GITHUB_TOKEN}"},
	})

	if failed {
		t.Fatalf("add_server failed: %s", text)
	}
	saved := readServerYAML(t, e.dir, "added")
	if saved.URL != upstream.URL {
		t.Errorf("saved url = %q, want %q", saved.URL, upstream.URL)
	}
	if !saved.BlockPrivateIPs {
		t.Error("saved server lacks block_private_ips, so a restart would drop the private-address check")
	}
	if len(saved.Headers) != 0 || saved.Auth != nil {
		t.Errorf("saved headers %v / auth %+v, want the agent's credentials stripped", saved.Headers, saved.Auth)
	}
}

func TestConfigAddServer_existingName_isRefusedAndLeftRunning(t *testing.T) {
	cases := []struct {
		name            string
		configure       func(e configToolEnv)
		assertUntouched func(e configToolEnv)
	}{
		{
			name: "server file",
			configure: func(e configToolEnv) {
				writeServerYAML(e.t, e.dir, "svc", "name: svc\ntransport: http\nurl: https://real.example.com/mcp\n")
			},
			assertUntouched: func(e configToolEnv) {
				if got := readServerYAML(e.t, e.dir, "svc").URL; got != "https://real.example.com/mcp" {
					e.t.Errorf("servers/svc.yaml url = %q, the refused add_server rewrote it", got)
				}
			},
		},
		{
			name: "inline in config.yaml",
			configure: func(e configToolEnv) {
				writeReloadFile(e.t, filepath.Join(e.dir, "config.yaml"), "servers:\n- name: svc\n  transport: http\n  url: https://real.example.com/mcp\n")
			},
			assertUntouched: func(e configToolEnv) { e.assertNoServerFile("svc") },
		},
		{
			name:            "running without config",
			configure:       func(e configToolEnv) {},
			assertUntouched: func(e configToolEnv) { e.assertNoServerFile("svc") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newConfigToolEnv(t)
			tc.configure(e)
			running := fakeConn("real_tool")
			addEdgeConn(t, e.srv, config.ServerConfig{Name: "svc"}, running)
			upstream := newMCPTestServer(t, pingTools)

			text, failed := e.addServer(map[string]any{"name": "svc", "transport": "http", "url": upstream.URL})

			if !failed || !strings.Contains(text, "remove it with remove_server first") {
				t.Fatalf("add_server = %q, want a refusal pointing at remove_server", text)
			}
			if running.Closed {
				t.Error("refused add_server disconnected the configured svc")
			}
			tc.assertUntouched(e)
		})
	}
}

func TestConfigAddServer_connectFails_savesNothing(t *testing.T) {
	e := newConfigToolEnv(t)

	_, failed := e.addServer(map[string]any{"name": "down", "transport": "http", "url": "http://127.0.0.1:1/mcp"})

	if !failed {
		t.Fatal("add_server to an unreachable URL succeeded")
	}
	e.assertNoServerFile("down")
}

func TestConfigAddServer_envReference_isRefusedSoARestartCantExpandIt(t *testing.T) {
	e := newConfigToolEnv(t)
	upstream := newMCPTestServer(t, pingTools)

	text, failed := e.addServer(map[string]any{"name": "leak", "transport": "http", "url": upstream.URL + "/?t=${HOME}"})

	if !failed || !strings.Contains(text, "environment variable") {
		t.Fatalf("add_server = %q, want an environment-variable refusal", text)
	}
	e.assertNoServerFile("leak")
}

func TestConfigAddServer_reusingARemovedName_isNeverSentItsToken(t *testing.T) {
	e := newConfigToolEnv(t)
	writeServerYAML(t, e.dir, "svc", "name: svc\ntransport: http\nurl: https://trusted.example/mcp\nauth:\n  type: oauth2\n")
	if err := auth.Save(e.dir, "svc", &oauth2.Token{AccessToken: "user-token", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	attacker, authHeaders := newRecordingMCPServer(t)

	if text, failed := e.removeServer("svc"); failed {
		t.Fatalf("remove_server: %s", text)
	}
	if text, failed := e.addServer(map[string]any{"name": "svc", "transport": "http", "url": attacker.URL, "command": "server-slack"}); failed {
		t.Fatalf("add_server: %s", text)
	}
	restartWithSavedServers(t, e.dir)

	for _, header := range authHeaders() {
		if header != "" {
			t.Fatalf("the agent's server received Authorization %q after a restart", header)
		}
	}
}

func newRecordingMCPServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var headers []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Get("Authorization"))
		mu.Unlock()
		fakeMCPHandle(w, r, pingTools)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(headers)
	}
}

func restartWithSavedServers(t *testing.T, dir string) {
	t.Helper()
	cfg, servers, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DangerousAllowPrivateURLs = true
	restarted := newTestServer(t, server.Params{Config: cfg, ConfigDir: dir})
	for _, sc := range servers {
		restarted.AddUpstream(t.Context(), sc) //nolint:errcheck
	}
}

func TestConfigAddServer_overlappingAddsOfOneName_leaveTheWinnerRunning(t *testing.T) {
	e := newConfigToolEnv(t)
	slow, inHandshake, finishHandshake := newGatedMCPServer(t)
	slowResult := make(chan string, 1)
	go func() {
		text, _ := e.addServer(map[string]any{"name": "svc", "transport": "http", "url": slow.URL})
		slowResult <- text
	}()
	<-inHandshake
	winner := newMCPTestServer(t, pingTools)

	if text, failed := e.addServer(map[string]any{"name": "svc", "transport": "http", "url": winner.URL}); failed {
		t.Fatalf("add_server: %s", text)
	}
	finishHandshake()

	if text := <-slowResult; !strings.Contains(text, "already") {
		t.Fatalf("overlapping add_server = %q, want it refused", text)
	}
	if e.srv.ToolCount("svc") == 0 {
		t.Error("the refused overlapping add disconnected the saved server")
	}
	if saved := readServerYAML(t, e.dir, "svc"); saved.URL != winner.URL {
		t.Errorf("saved url = %q, want the winner's %q", saved.URL, winner.URL)
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
		upstream := newMCPTestServer(t, pingTools)
		if text, failed := e.addServer(map[string]any{"name": "svc", "transport": "http", "url": upstream.URL}); failed {
			t.Fatalf("add_server: %s", text)
		}

		if text, failed := e.removeServer("svc"); failed {
			t.Fatalf("remove_server: %s", text)
		}

		e.assertNoServerFile("svc")
		if e.srv.ToolCount("svc") != 0 {
			t.Error("svc still connected after remove_server")
		}
	})
	t.Run("refuses a server defined inline in config.yaml", func(t *testing.T) {
		e := newConfigToolEnv(t)
		writeReloadFile(t, filepath.Join(e.dir, "config.yaml"), "servers:\n- name: svc\n  command: echo\n")
		addEdgeConn(t, e.srv, config.ServerConfig{Name: "svc"}, fakeConn("ping"))

		text, failed := e.removeServer("svc")

		if !failed || !strings.Contains(text, "config.yaml") {
			t.Fatalf("remove_server = %q, want an error pointing at config.yaml", text)
		}
		if e.srv.ToolCount("svc") == 0 {
			t.Error("refused remove_server still disconnected svc")
		}
	})
	t.Run("disconnects a server with no config", func(t *testing.T) {
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
