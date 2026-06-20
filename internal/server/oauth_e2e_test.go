//go:build test

package server_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func oauthMCPHandler(validToken string, tools []map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+validToken {
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
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
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
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		listText := toolResultText(t, serve(t, srv, callTool("list", map[string]any{})))
		var tools []any
		if err := json.Unmarshal([]byte(listText), &tools); err == nil && len(tools) == 2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for tools to be accessible after OAuth flow")
}
