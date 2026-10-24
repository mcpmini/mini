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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/transport"

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
		if serveOAuthMetadata(w, r) {
			return
		}
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
				"jsonrpc": transport.JsonRPCVersion, "id": id,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "protected", "version": "0"},
				},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": transport.JsonRPCVersion, "id": id,
				"result": map[string]any{"tools": tools},
			})
		default:
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": id, "result": nil})
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
		if serveOAuthMetadata(w, r) {
			return
		}
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

type newOAuthServerParams struct {
	T          *testing.T
	ConfigDir  string
	ServerName string
	TokenURL   string
	MCPURL     string
}

func newOAuthServer(params newOAuthServerParams) *server.Server {
	params.T.Helper()
	configtest.WriteServer(params.T, params.ConfigDir, config.ServerConfig{
		Name:      params.ServerName,
		Transport: "http",
		URL:       params.MCPURL,
		Auth: &config.AuthConfig{
			Type:     "oauth2",
			Issuer:   params.MCPURL,
			ClientID: "test-client",
			AuthURL:  params.TokenURL + "/authorize",
			TokenURL: params.TokenURL + "/token",
		},
	})
	cfg := config.DefaultConfig()
	cfg.DisableAuthBrowserOpen = true
	return newTestServer(params.T, server.Params{Config: cfg, ConfigDir: params.ConfigDir})
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
	srv := newOAuthServer(
		newOAuthServerParams{
			T:          t,
			ConfigDir:  t.TempDir(),
			ServerName: "protected",
			TokenURL:   tokenSrv.URL,
			MCPURL:     mcpSrv.URL,
		},
	)
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
			Issuer:     tokenSrv.URL,
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
	srv := newOAuthServer(
		newOAuthServerParams{
			T:          t,
			ConfigDir:  t.TempDir(),
			ServerName: "mysvc",
			TokenURL:   tokenSrv.URL,
			MCPURL:     mcpSrv.URL,
		},
	)
	authText := toolResultText(
		t,
		serve(t, srv, callTool("config", map[string]any{"action": "start_auth", "server": "mysvc"})),
	)
	var authResult map[string]any
	json.Unmarshal([]byte(authText), &authResult)
	authtest.CompleteAuthorization(t, authResult["url"].(string), "test-code")
	for range 100 {
		listText := toolListJSON(t, serve(t, srv, rpc("tools/list", nil)))
		if strings.Contains(listText, `"mysvc__search"`) && strings.Contains(listText, `"mysvc__create"`) {
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

	var mu sync.Mutex
	acceptedToken := ""
	var unauthorizedAfterAuth atomic.Int32

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveOAuthMetadata(w, r) {
			return
		}
		mu.Lock()
		vt := acceptedToken
		mu.Unlock()
		if vt == "" || r.Header.Get("Authorization") != "Bearer "+vt {
			if r.Method == http.MethodPost {
				unauthorizedAfterAuth.Add(1)
			}
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		id := req["id"]
		switch req["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": id, //nolint:errcheck
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "srv", "version": "0"},
				}})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": id, //nolint:errcheck
				"result": map[string]any{"tools": []map[string]any{
					{"name": "getData", "description": "get data", "inputSchema": map[string]any{"type": "object"}},
				}}})
		default:
			json.NewEncoder(w).
				Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": id, "result": nil})
			//nolint:errcheck
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

	sc := config.ServerConfig{
		Name:      "srv",
		Transport: "http",
		URL:       mcpSrv.URL,
		Auth: &config.AuthConfig{
			Type:     "oauth2",
			Issuer:   mcpSrv.URL,
			ClientID: "test-client",
			AuthURL:  tokenSrv.URL + "/authorize",
			TokenURL: tokenSrv.URL + "/token",
		},
	}
	configtest.WriteServer(t, dir, sc)
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir: dir, ServerName: "srv", Server: sc, Token: &oauth2.Token{AccessToken: staleToken},
	})

	cfg := config.DefaultConfig()
	cfg.DisableAuthBrowserOpen = true
	mini := newTestServer(t, server.Params{Config: cfg, ConfigDir: dir})
	defer mini.Close()

	sc = loadServerConfig(t, dir, "srv")
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

	serve(t, mini, rpc("tools/list", nil))

	if n := unauthorizedAfterAuth.Load(); n > 0 {
		t.Errorf("browser token caused %d unexpected 401 responses after auth, want 0", n)
	}
}

func serveOAuthMetadata(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/.well-known/oauth-authorization-server" {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"issuer":                           "http://" + r.Host,
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	return true
}
