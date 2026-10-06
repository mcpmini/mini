//go:build test

package server_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

func newServerWithDir(t *testing.T, configDir string) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.DisableAuthBrowserOpen = true
	return newTestServer(t, server.Params{Config: cfg, ConfigDir: configDir})
}

func configureResult(t *testing.T, srv *server.Server, args map[string]any) map[string]any {
	t.Helper()
	resp := serve(t, srv, callTool("config", args))
	text := toolResultText(t, resp)
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON result, got: %s", text)
	}
	return result
}

func TestAuthStatus_noToken_returnsUnauthorized(t *testing.T) {
	dir := t.TempDir()
	srv := newServerWithDir(t, dir)

	result := configureResult(t, srv, map[string]any{
		"action": "auth_status",
		"server": "myserver",
	})

	if result["authorized"] != false {
		t.Errorf("expected authorized=false for missing token, got: %v", result["authorized"])
	}
	if result["server"] != "myserver" {
		t.Errorf("expected server=myserver, got: %v", result["server"])
	}
}

func TestAuthStatus_validToken_returnsAuthorized(t *testing.T) {
	dir := t.TempDir()
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir:  dir,
		ServerName: "myserver",
		Token: &oauth2.Token{
			AccessToken:  "test-access-token",
			RefreshToken: "test-refresh-token",
			TokenType:    "Bearer",
			Expiry:       time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	})

	result := configureResult(t, newServerWithDir(t, dir), map[string]any{
		"action": "auth_status",
		"server": "myserver",
	})
	if result["authorized"] != true {
		t.Errorf("expected authorized=true for valid token, got: %v", result["authorized"])
	}
	if result["expires"] == nil {
		t.Error("expected non-nil expires for token with expiry")
	}
}

func TestAuthStatus_expiredToken_returnsUnauthorized(t *testing.T) {
	dir := t.TempDir()
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir:  dir,
		ServerName: "myserver",
		Token: &oauth2.Token{
			AccessToken: "old-token",
			TokenType:   "Bearer",
			Expiry:      time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	})

	result := configureResult(t, newServerWithDir(t, dir), map[string]any{
		"action": "auth_status",
		"server": "myserver",
	})
	if result["authorized"] != false {
		t.Errorf("expected authorized=false for expired token, got: %v", result["authorized"])
	}
}

func TestAuthStatus_invalidServerName_returnsError(t *testing.T) {
	dir := t.TempDir()
	srv := newServerWithDir(t, dir)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "auth_status",
		"server": "bad name!",
	}))
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true for invalid server name, got: %v", result)
	}
}

func TestStartAuth_invalidServerName_returnsError(t *testing.T) {
	dir := t.TempDir()
	srv := newServerWithDir(t, dir)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "start_auth",
		"server": "bad name!",
	}))
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true for invalid server name, got: %v", result)
	}
}

func TestStartAuth_serverNotFound_returnsError(t *testing.T) {
	dir := t.TempDir()
	srv := newServerWithDir(t, dir)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "start_auth",
		"server": "nonexistent",
	}))
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true for missing server, got: %v", result)
	}
}

func TestStartAuth_noOAuthConfig_returnsError(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "plain", Command: "echo hello"})
	srv := newServerWithDir(t, dir)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "start_auth",
		"server": "plain",
	}))
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true for server without oauth2, got: %v", result)
	}
}

func TestStartAuth_loadsTheNamedServerWhenAnotherServerFileIsBroken(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "plain", Command: "echo hello"})
	testutil.WriteFile(t, config.ServerPath(dir, "other"), "transport: http\nurl: [unfinished\n")
	srv := newServerWithDir(t, dir)

	resp := serve(t, srv, callTool("config", map[string]any{"action": "start_auth", "server": "plain"}))
	if text := toolResultText(t, resp); !strings.Contains(text, `"plain" does not have oauth2 auth configured`) {
		t.Errorf("start_auth plain = %q, want the OAuth check on plain's own config", text)
	}
}
