//go:build test

package server_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

const reconnectedLog = "reconnecting server with its changed settings"

var otherTools = []map[string]any{
	{"name": "pong", "description": "pong", "inputSchema": map[string]any{"type": "object"}},
}

func TestServerReload_editedHeader_reconnectsOnce(t *testing.T) {
	e := newServerReloadEnv(t)
	e.startWithServers("svc")

	e.writeServer(config.ServerConfig{
		Name: "svc", Transport: "http", URL: e.upstream.URL, Headers: map[string]string{"X-Key": "rotated"},
	})
	e.advanceTick()
	e.srv.WaitForStartupConnects()
	e.advanceTick()

	e.assertConnected("svc")
	if got := strings.Count(e.logs.String(), reconnectedLog); got != 1 {
		t.Errorf("svc reconnected %d times, want once", got)
	}
}

func TestServerReload_editedURL_servesTheNewServersTools(t *testing.T) {
	e := newServerReloadEnv(t)
	moved := newMCPTestServer(t, otherTools)
	e.startWithServers("svc")

	e.writeServer(config.ServerConfig{Name: "svc", Transport: "http", URL: moved.URL})
	e.advanceTick()
	e.srv.WaitForStartupConnects()

	tools := toolsList(t, e.srv)
	if !containsName(tools, "svc__pong") || containsName(tools, "svc__ping") {
		t.Errorf("tools = %v, want the moved server's svc__pong only", toolNames(tools))
	}
}

func TestServerReload_editsThatChangeNoSettings_keepTheConnection(t *testing.T) {
	edits := map[string]func(e *serverReloadEnv){
		"projections": func(e *serverReloadEnv) {
			e.writeServer(config.ServerConfig{
				Name: "svc", Transport: "http", URL: e.upstream.URL,
				Projections: map[string]*config.ProjectionConfig{"ping": {IncludeOnly: []string{"a"}}},
			})
		},
		"a comment": func(e *serverReloadEnv) {
			testutil.WriteFile(e.t, e.serverPath("svc"), "# moved here\ntransport: http\nurl: "+e.upstream.URL+"\n")
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			e := newServerReloadEnv(t)
			e.startWithServers("svc")

			edit(e)
			e.advanceTick()
			e.advanceTick()

			if strings.Contains(e.logs.String(), reconnectedLog) {
				t.Errorf("an edit that changes no settings reconnected svc; logs:\n%s", e.logs.String())
			}
			e.assertConnected("svc")
		})
	}
}

func TestServerReload_aHeaderFromTheEnvironment_isNotAnEdit(t *testing.T) {
	t.Setenv("MINI_TEST_KEY", "from-env")
	e := newServerReloadEnv(t)
	e.writeServer(config.ServerConfig{
		Name: "svc", Transport: "http", URL: e.upstream.URL, Headers: map[string]string{"X-Key": "${MINI_TEST_KEY}"},
	})
	e.connectConfigured()
	e.startPoller()
	e.advanceTick()

	if strings.Contains(e.logs.String(), reconnectedLog) {
		t.Errorf("a ${VAR} header made the first check reconnect svc; logs:\n%s", e.logs.String())
	}
}

func TestServerReload_aServerStillRetrying_reconnectsToTheFixedURL(t *testing.T) {
	e := newServerReloadEnv(t)
	gone := httptest.NewServer(nil)
	gone.Close()
	e.writeServer(config.ServerConfig{Name: "svc", Transport: "http", URL: gone.URL})
	e.srv.ConnectUpstreams(t.Context(), []config.ServerConfig{{Name: "svc", Transport: "http", URL: gone.URL}})
	e.waitForRetryBackoff()
	e.startPoller()

	e.writeServer(config.ServerConfig{Name: "svc", Transport: "http", URL: e.upstream.URL})
	e.advanceTick()

	eventually(t, func() bool { return e.srv.ToolCount("svc") > 0 })
}

func TestServerReload_trustingAServerAnAgentAdded_startsIt(t *testing.T) {
	e := newServerReloadEnv(t)
	echomcp := testutil.Binary(t, "ECHOMCP_BIN")
	e.writeServer(config.ServerConfig{Name: "svc", Command: echomcp, AgentAdded: true})
	e.connectConfigured()
	e.startPoller()
	e.assertRemoved("svc")

	e.writeServer(config.ServerConfig{Name: "svc", Command: echomcp})
	e.advanceTick()
	e.srv.WaitForStartupConnects()

	e.assertConnected("svc")
}

func TestServerReload_aLoginStartedAfterAnEdit_survivesAndIsKept(t *testing.T) {
	const accessToken = "edited-login"
	upstream := fakeOAuthMCPServer(t, accessToken, pingTools)
	tokens := fakeTokenServer(t, accessToken)
	cfg := config.DefaultConfig()
	cfg.DisableAuthBrowserOpen = true
	e := &serverReloadEnv{reloadEnv: buildReloadEnvWithConfig(t, evalTempDir(t), cfg)}
	oauthServer := func(scopes ...string) config.ServerConfig {
		return config.ServerConfig{Name: "svc", Transport: "http", URL: upstream.URL, Auth: &config.AuthConfig{
			Type: "oauth2", ClientID: "test-client", Scopes: scopes,
			AuthURL: tokens.URL + "/authorize", TokenURL: tokens.URL + "/token",
		}}
	}
	e.writeServer(oauthServer())
	e.startPoller()
	e.srv.WaitForStartupConnects()

	e.writeServer(oauthServer("read"))
	authURL := startAuth(t, e.srv, "svc")
	e.advanceTick()
	authtest.CompleteAuthorization(t, authURL, "code")
	waitForServerConnected(t, e.srv, "svc")
	e.advanceTick()

	if strings.Contains(e.logs.String(), reconnectedLog) {
		t.Errorf("a check reconnected svc around a login for its current file; logs:\n%s", e.logs.String())
	}
}

func startAuth(t *testing.T, srv *server.Server, name string) string {
	t.Helper()
	result := parseEnvelope(
		t,
		toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "start_auth", "server": name}))),
	)
	url, _ := result["url"].(string)
	if url == "" {
		t.Fatalf("start_auth = %v, want an authorization URL", result)
	}
	return url
}

func TestServerReload_anEditThatFailsToConnect_tellsAgentsTheToolsAreGone(t *testing.T) {
	e := newServerReloadEnv(t)
	e.startWithServers("svc")
	agent := openAgent(t, e.srv)

	e.writeServer(config.ServerConfig{
		Name:      "svc",
		Transport: "http",
		URL:       e.upstream.URL,
		Headers:   map[string]string{"X-Key": "${MINI_TEST_UNSET_KEY}"},
	})
	e.advanceTick()

	agent.waitForLine("notifications/tools/list_changed")
}
