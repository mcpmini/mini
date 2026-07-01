//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

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

// visitCallback simulates a browser completing the OAuth flow by hitting
// the local callback URL with the code and state from the auth URL.
func visitCallback(authURL string) error {
	parsed, err := url.Parse(authURL)
	if err != nil {
		return fmt.Errorf("parse auth URL: %w", err)
	}
	q := parsed.Query()
	state := q.Get("state")
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" {
		return fmt.Errorf("missing redirect_uri in auth URL: %s", authURL)
	}
	callbackURL := redirectURI + "?code=test-code&state=" + url.QueryEscape(state)
	go http.Get(callbackURL) //nolint:errcheck
	return nil
}

func newOAuthServer(t *testing.T, dir, svcName, tokenURL, mcpURL string) *server.Server {
	t.Helper()
	writeServerYAML(t, dir, svcName, fmt.Sprintf("name: %s\ntransport: http\nurl: %s\nauth:\n  type: oauth2\n  client_id: test-client\n  auth_url: %s/authorize\n  token_url: %s/token\n",
		svcName, mcpURL, tokenURL, tokenURL))
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DisableAuthBrowserOpen = true
	return server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	if err := visitCallback(authURL); err != nil {
		t.Fatalf("simulate browser: %v", err)
	}
	waitForServerConnected(t, srv, "protected")
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
	visitCallback(authResult["url"].(string)) //nolint:errcheck
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
	writeServerYAML(t, dir, "needsauth", "name: needsauth\ntransport: http\nurl: "+mcpSrv.URL+"\n")
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

	got := readServerYAML(t, dir, "needsauth")
	if got.Auth == nil || got.Auth.Type != "oauth2" {
		t.Errorf("Auth = %+v, want type oauth2 persisted to yaml", got.Auth)
	}
}

func TestAddUpstream_doesNotOverwriteExistingAuth(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	writeServerYAML(t, dir, "hasauth", "name: hasauth\ntransport: http\nurl: "+mcpSrv.URL+"\nauth:\n  type: apikey\n  token: secret\n")
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
	writeServerYAML(t, dir, "plain401", "name: plain401\ntransport: http\nurl: "+mcpSrv.URL+"\n")
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

	got := readServerYAML(t, dir, "plain401")
	if got.Auth != nil {
		t.Errorf("Auth = %+v, expected no auth field written", got.Auth)
	}
}

func TestAddUpstream_staticBearerHeaderIsNotMisclassifiedAsOAuth(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	writeServerYAML(t, dir, "statictoken", "name: statictoken\ntransport: http\nurl: "+mcpSrv.URL+"\nheaders:\n  Authorization: Bearer some-static-token\n")
	srv := newServerWithDir(t, dir)
	defer srv.Close()

	sc := loadServerConfig(t, dir, "statictoken")
	if err := srv.AddUpstream(context.Background(), sc); err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}

	got := readServerYAML(t, dir, "statictoken")
	if got.Auth != nil {
		t.Errorf("Auth = %+v, a server with a manually-configured Authorization header must never be marked oauth2 — RFC 6750 mandates the same Bearer challenge for an expired static token", got.Auth)
	}
}

func TestAddUpstream_customAuthHeaderIsNotMisclassifiedAsOAuth(t *testing.T) {
	// A server authenticating via a non-Authorization header (e.g. X-Api-Key) is just as
	// vulnerable to the RFC 6750 ambiguity as one using Authorization — an expired static
	// key server and an OAuth-protected server can both answer a 401 with WWW-Authenticate:
	// Bearer, so any manually-configured header must be treated as decisive evidence of an
	// already-chosen auth mechanism, not just the literal "Authorization" key.
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	writeServerYAML(t, dir, "apikeyserver", "name: apikeyserver\ntransport: http\nurl: "+mcpSrv.URL+"\nheaders:\n  X-Api-Key: some-static-key\n")
	srv := newServerWithDir(t, dir)
	defer srv.Close()

	sc := loadServerConfig(t, dir, "apikeyserver")
	if err := srv.AddUpstream(context.Background(), sc); err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}

	got := readServerYAML(t, dir, "apikeyserver")
	if got.Auth != nil {
		t.Errorf("Auth = %+v, a server with any manually-configured header must never be marked oauth2", got.Auth)
	}
}

func TestAddUpstream_runtimeAddedNeverPersistsToDisk(t *testing.T) {
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	dir := t.TempDir()
	writeServerYAML(t, dir, "collide", "name: collide\ntransport: http\nurl: https://real-server.example.com/mcp\n")
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DisableAuthBrowserOpen = true
	// DangerousAllowPrivateURLs lets the dial reach the loopback httptest server so the
	// test actually exercises markOAuthIfRequired's RuntimeAdded guard, rather than
	// failing earlier at SSRF dial validation for an unrelated reason.
	cfg.DangerousAllowPrivateURLs = true
	srv := server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	runtimeSC := config.ServerConfig{Name: "collide", Transport: "http", URL: mcpSrv.URL, RuntimeAdded: true}
	if err := srv.AddUpstream(context.Background(), runtimeSC); err == nil {
		t.Fatal("expected AddUpstream to return an error")
	}

	got := readServerYAML(t, dir, "collide")
	if got.Auth != nil {
		t.Errorf("Auth = %+v, runtime-added server must never rewrite an existing server's config", got.Auth)
	}
	if got.URL != "https://real-server.example.com/mcp" {
		t.Errorf("URL = %q, real server config was overwritten", got.URL)
	}
}
