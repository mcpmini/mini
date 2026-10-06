//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

func oauthMCPHandler(validToken string, tools []map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+validToken {
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		id := req["id"]
		switch req["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "protected", "version": "0"},
				},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{"tools": tools},
			})
		default:
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": nil})
		}
	}
}

func fakeOAuthMCPServer(t *testing.T, validToken string, tools []map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(oauthMCPHandler(validToken, tools))
	t.Cleanup(srv.Close)
	return srv
}

// fakeTokenServer returns an OAuth2 token server that issues the given access token.
func fakeTokenServer(t *testing.T, accessToken string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  accessToken,
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "refresh-" + accessToken,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newOAuthServer(t *testing.T, dir, svcName, tokenURL, mcpURL string) *server.Server {
	t.Helper()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      svcName,
		Transport: "http",
		URL:       mcpURL,
		Auth: &config.AuthConfig{
			Type:     "oauth2",
			ClientID: "test-client",
			AuthURL:  tokenURL + "/authorize",
			TokenURL: tokenURL + "/token",
		},
	})
	cfg := config.DefaultConfig()
	cfg.DisableAuthBrowserOpen = true
	return newTestServer(t, server.Params{Config: cfg, ConfigDir: dir})
}

func waitForServerConnected(t *testing.T, srv *server.Server, svcName string) {
	t.Helper()
	for range 100 {
		statusText := toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "status"})))
		var status map[string]any
		if err := json.Unmarshal([]byte(statusText), &status); err == nil {
			if servers, ok := status["servers"].(map[string]any); ok {
				if _, connected := servers[svcName]; connected {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to connect after OAuth flow", svcName)
}

func TestStartAuth_e2e_connectsAfterOAuthFlow(t *testing.T) {
	const accessToken = "e2e-valid-token"
	tokenSrv := fakeTokenServer(t, accessToken)
	mcpSrv := fakeOAuthMCPServer(t, accessToken, []map[string]any{
		{"name": "getData", "description": "get data", "inputSchema": map[string]any{"type": "object"}},
	})
	srv := newOAuthServer(t, t.TempDir(), "protected", tokenSrv.URL, mcpSrv.URL)
	resp := serve(t, srv, callTool("config", map[string]any{"action": "start_auth", "server": "protected"}))
	authResult := parseEnvelope(t, toolResultText(t, resp))
	if authResult["ok"] != true {
		t.Fatalf("start_auth failed: %v", authResult)
	}
	authURL, _ := authResult["url"].(string)
	if authURL == "" {
		t.Fatal("expected non-empty auth URL from start_auth")
	}
	authtest.CompleteAuthorization(t, authURL, "test-code")
	waitForServerConnected(t, srv, "protected")
}

func TestStartAuth_opensServerBrowserCommandWithAuthURL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only: the browser command uses shell redirection")
	}
	dir := t.TempDir()
	openedPath := filepath.Join(dir, "opened-url")
	tokenSrv := fakeTokenServer(t, "unused-token")
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "protected",
		Transport: "http",
		URL:       tokenSrv.URL + "/mcp",
		Auth: &config.AuthConfig{
			Type:       "oauth2",
			ClientID:   "test-client",
			AuthURL:    tokenSrv.URL + "/authorize",
			TokenURL:   tokenSrv.URL + "/token",
			BrowserCmd: "printf %s > " + openedPath,
		},
	})
	cfg := config.DefaultConfig()
	cfg.BrowserCommand = "false"
	srv := newTestServer(t, server.Params{Config: cfg, ConfigDir: dir})

	authResult := parseEnvelope(
		t,
		toolResultText(
			t,
			serve(t, srv, callTool("config", map[string]any{"action": "start_auth", "server": "protected"})),
		),
	)

	authURL, _ := authResult["url"].(string)
	if authURL == "" {
		t.Fatalf("start_auth = %v, want an auth URL", authResult)
	}
	if opened := waitForFileContent(t, openedPath); opened != authURL {
		t.Errorf("browser opened %q, want the start_auth URL %q", opened, authURL)
	}
}

func waitForFileContent(t *testing.T, path string) string {
	t.Helper()
	for range 100 {
		data, err := os.ReadFile(path) //fileiolint:allow poll for output from the browser process
		if err == nil &&
			len(data) > 0 {
			return string(data)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
	return ""
}

func TestStartAuth_e2e_toolsAccessibleAfterAuth(t *testing.T) {
	const accessToken = "e2e-tools-token"
	tokenSrv := fakeTokenServer(t, accessToken)
	mcpSrv := fakeOAuthMCPServer(t, accessToken, []map[string]any{
		{"name": "search", "description": "search things", "inputSchema": map[string]any{"type": "object"}},
		{"name": "create", "description": "create thing", "inputSchema": map[string]any{"type": "object"}},
	})
	srv := newOAuthServer(t, t.TempDir(), "mysvc", tokenSrv.URL, mcpSrv.URL)
	authText := toolResultText(
		t,
		serve(t, srv, callTool("config", map[string]any{"action": "start_auth", "server": "mysvc"})),
	)
	var authResult map[string]any
	json.Unmarshal([]byte(authText), &authResult)
	authtest.CompleteAuthorization(t, authResult["url"].(string), "test-code")
	for range 100 {
		listText := toolResultText(t, serve(t, srv, callTool("list", map[string]any{})))
		var tools []any
		if err := json.Unmarshal([]byte(listText), &tools); err == nil && len(tools) == 2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for tools to be accessible after OAuth flow")
}

func loadServerConfig(t *testing.T, dir, name string) config.ServerConfig {
	t.Helper()
	sc, err := config.LoadServer(dir, name)
	if err != nil {
		t.Fatalf("config.LoadServer: %v", err)
	}
	return sc
}

func readServerYAML(t *testing.T, dir, name string) config.ServerConfig {
	t.Helper()
	var sc config.ServerConfig
	readYAMLFile(t, filepath.Join(dir, "servers", name+".yaml"), &sc)
	return sc
}

func readYAMLFile(t *testing.T, path string, out any) {
	t.Helper()
	data := testutil.ReadFile(t, path)
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("yaml.Unmarshal %s: %v", path, err)
	}
}

func TestStartAuth_e2e_withStaleToken_browserTokenUsedOnFirstRequest(t *testing.T) {
	const staleToken = "stale-token"
	const browserToken = "browser-token"

	dir := t.TempDir()
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir:  dir,
		ServerName: "srv",
		Token: &oauth2.Token{
			AccessToken: staleToken,
		},
	})

	var mu sync.Mutex
	acceptedToken := ""
	var unauthorizedAfterAuth atomic.Int32

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		vt := acceptedToken
		mu.Unlock()
		if vt == "" || r.Header.Get("Authorization") != "Bearer "+vt {
			unauthorizedAfterAuth.Add(1)
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		id := req["id"]
		switch req["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, //nolint:errcheck
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "srv", "version": "0"},
				}})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, //nolint:errcheck
				"result": map[string]any{"tools": []map[string]any{
					{"name": "getData", "description": "get data", "inputSchema": map[string]any{"type": "object"}},
				}}})
		default:
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": nil}) //nolint:errcheck
		}
	}))
	defer mcpSrv.Close()

	var tokenEndpointOpen atomic.Bool
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !tokenEndpointOpen.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": browserToken, "token_type": "Bearer",
			"expires_in": 3600, "refresh_token": "refresh-" + browserToken,
		})
	}))
	defer tokenSrv.Close()

	configtest.WriteServer(t, dir, config.ServerConfig{
		Name:      "srv",
		Transport: "http",
		URL:       mcpSrv.URL,
		Auth: &config.AuthConfig{
			Type:     "oauth2",
			ClientID: "test-client",
			AuthURL:  tokenSrv.URL + "/authorize",
			TokenURL: tokenSrv.URL + "/token",
		},
	})

	cfg := config.DefaultConfig()
	cfg.DisableAuthBrowserOpen = true
	mini := newTestServer(t, server.Params{Config: cfg, ConfigDir: dir})
	defer mini.Close()

	sc := loadServerConfig(t, dir, "srv")
	if err := mini.AddUpstream(context.Background(), sc); err == nil {
		t.Fatal("initial dial with the stale token should fail, leaving a provider registered")
	}

	tokenEndpointOpen.Store(true)
	mu.Lock()
	acceptedToken = browserToken
	mu.Unlock()
	unauthorizedAfterAuth.Store(0)

	authText := toolResultText(
		t,
		serve(t, mini, callTool("config", map[string]any{"action": "start_auth", "server": "srv"})),
	)
	var authResult map[string]any
	if err := json.Unmarshal([]byte(authText), &authResult); err != nil {
		t.Fatalf("parse start_auth response: %v", err)
	}
	authtest.CompleteAuthorization(t, authResult["url"].(string), "test-code")

	waitForServerConnected(t, mini, "srv")

	serve(t, mini, callTool("list", map[string]any{}))

	if n := unauthorizedAfterAuth.Load(); n > 0 {
		t.Errorf("browser token caused %d unexpected 401 responses after auth, want 0", n)
	}
}
