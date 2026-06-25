//go:build test

package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func newServerWithDir(t *testing.T, configDir string) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DisableAuthBrowserOpen = true
	return server.NewWithConfigDir(cfg, configDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func writeServerYAML(t *testing.T, dir, name, content string) {
	t.Helper()
	serversDir := filepath.Join(dir, "servers")
	if err := os.MkdirAll(serversDir, 0700); err != nil {
		t.Fatalf("mkdir servers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(serversDir, name+".yaml"), []byte(content), 0600); err != nil {
		t.Fatalf("write server yaml: %v", err)
	}
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

func saveToken(t *testing.T, dir, serverName string, tok *oauth2.Token) {
	t.Helper()
	if err := auth.Save(dir, serverName, tok); err != nil {
		t.Fatalf("save token: %v", err)
	}
}

func TestAuthStatus_validToken_returnsAuthorized(t *testing.T) {
	dir := t.TempDir()
	saveToken(t, dir, "myserver", &oauth2.Token{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
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
	saveToken(t, dir, "myserver", &oauth2.Token{
		AccessToken: "old-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(-time.Hour),
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
	writeServerYAML(t, dir, "plain", `name: plain
command: echo hello
`)
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
