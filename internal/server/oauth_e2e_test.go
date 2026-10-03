//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
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

	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
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
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id,
				"result": map[string]any{"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{"tools": map[string]any{}},
					"serverInfo":   map[string]any{"name": "protected", "version": "0"}}})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id,
				"result": map[string]any{"tools": tools}})
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
	writeServerYAML(t, dir, svcName, fmt.Sprintf("transport: http\nurl: %s\nauth:\n  type: oauth2\n  client_id: test-client\n  auth_url: %s/authorize\n  token_url: %s/token\n",
		mcpURL, tokenURL, tokenURL))
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
	writeServerYAML(t, dir, "protected", fmt.Sprintf("transport: http\nurl: %s/mcp\nauth:\n  type: oauth2\n  client_id: test-client\n  auth_url: %s/authorize\n  token_url: %s/token\n  browser_cmd: printf %%s > %s\n",
		tokenSrv.URL, tokenSrv.URL, tokenSrv.URL, openedPath))
	cfg := config.DefaultConfig()
	cfg.BrowserCommand = "false"
	srv := newTestServer(t, server.Params{Config: cfg, ConfigDir: dir})

	authResult := parseEnvelope(t, toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "start_auth", "server": "protected"}))))

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
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
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
	authText := toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "start_auth", "server": "mysvc"})))
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
	_, servers, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	sc := config.FindServer(servers, name)
	if sc == nil {
		t.Fatalf("server %q not found after config.Load", name)
	}
	return *sc
}

func readServerYAML(t *testing.T, dir, name string) config.ServerConfig {
	t.Helper()
	var sc config.ServerConfig
	readYAMLFile(t, filepath.Join(dir, "servers", name+".yaml"), &sc)
	return sc
}

func readYAMLFile(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		t.Fatalf("yaml.Unmarshal %s: %v", path, err)
	}
}

func TestAddUpstream_detectsOAuthFrom401(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	writeServerYAML(t, dir, "needsauth", "transport: http\nurl: "+mcpSrv.URL+"\n")
	srv := newServerWithDir(t, dir)
	defer srv.Close()

	sc := loadServerConfig(t, dir, "needsauth")
	err := srv.AddUpstream(context.Background(), sc)
	if err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}
	if !strings.Contains(err.Error(), "mini auth needsauth") {
		t.Errorf("error should mention `mini auth needsauth`, got: %v", err)
	}

	if !config.IsOAuthDetected(dir, "needsauth") {
		t.Error("expected the oauth-detected marker to be written")
	}
	got := loadServerConfig(t, dir, "needsauth")
	if got.Auth == nil || got.Auth.Type != "oauth2" {
		t.Errorf("Auth = %+v, want type oauth2 merged in from the detected marker", got.Auth)
	}
}

func TestAddUpstream_doesNotOverwriteExistingAuth(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	writeServerYAML(t, dir, "hasauth", "transport: http\nurl: "+mcpSrv.URL+"\nauth:\n  type: apikey\n  token: secret\n")
	srv := newServerWithDir(t, dir)
	defer srv.Close()

	sc := loadServerConfig(t, dir, "hasauth")
	if err := srv.AddUpstream(context.Background(), sc); err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}

	got := readServerYAML(t, dir, "hasauth")
	if got.Auth == nil || got.Auth.Type != "apikey" {
		t.Errorf("Auth = %+v, existing apikey config was clobbered", got.Auth)
	}
}

func TestAddUpstream_bare401WithNoEvidenceDoesNotMarkOAuth(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	writeServerYAML(t, dir, "plain401", "transport: http\nurl: "+mcpSrv.URL+"\n")
	srv := newServerWithDir(t, dir)
	defer srv.Close()

	sc := loadServerConfig(t, dir, "plain401")
	err := srv.AddUpstream(context.Background(), sc)
	if err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}
	if strings.Contains(err.Error(), "requires OAuth authorization") {
		t.Errorf("error should not claim OAuth is required, got: %v", err)
	}

	if config.IsOAuthDetected(dir, "plain401") {
		t.Error("a bare 401 with no PRM/header evidence must not write the oauth-detected marker")
	}
}

func TestAddUpstream_staticBearerHeaderIsNotMisclassifiedAsOAuth(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	writeServerYAML(t, dir, "statictoken", "transport: http\nurl: "+mcpSrv.URL+"\nheaders:\n  Authorization: Bearer some-static-token\n")
	srv := newServerWithDir(t, dir)
	defer srv.Close()

	sc := loadServerConfig(t, dir, "statictoken")
	if err := srv.AddUpstream(context.Background(), sc); err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}

	if config.IsOAuthDetected(dir, "statictoken") {
		t.Error("a server with a manually-configured Authorization header must never be marked oauth2 — RFC 6750 mandates the same Bearer challenge for an expired static token")
	}
}

func TestAddUpstream_customAuthHeaderIsNotMisclassifiedAsOAuth(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	writeServerYAML(t, dir, "apikeyserver", "transport: http\nurl: "+mcpSrv.URL+"\nheaders:\n  X-Api-Key: some-static-key\n")
	srv := newServerWithDir(t, dir)
	defer srv.Close()

	sc := loadServerConfig(t, dir, "apikeyserver")
	if err := srv.AddUpstream(context.Background(), sc); err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}

	if config.IsOAuthDetected(dir, "apikeyserver") {
		t.Error("a server with any manually-configured header must never be marked oauth2")
	}
}

func TestStartAuth_e2e_withStaleToken_browserTokenUsedOnFirstRequest(t *testing.T) {
	const staleToken = "stale-token"
	const browserToken = "browser-token"

	dir := t.TempDir()
	if err := auth.Save(dir, "srv", &oauth2.Token{
		AccessToken: staleToken,
	}); err != nil {
		t.Fatal(err)
	}

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
				"result": map[string]any{"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{"tools": map[string]any{}},
					"serverInfo":   map[string]any{"name": "srv", "version": "0"}}})
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

	writeServerYAML(t, dir, "srv", fmt.Sprintf(
		"transport: http\nurl: %s\nauth:\n  type: oauth2\n  client_id: test-client\n  auth_url: %s/authorize\n  token_url: %s/token\n",
		mcpSrv.URL, tokenSrv.URL, tokenSrv.URL))

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

	authText := toolResultText(t, serve(t, mini, callTool("config", map[string]any{"action": "start_auth", "server": "srv"})))
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
