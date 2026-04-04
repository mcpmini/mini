package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestConfigureReload_emptyDir(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	srv := server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))

	resp := serve(t, srv, callTool("config", map[string]any{"action": "reload"}))
	text := toolResultText(t, resp)

	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON from reload, got: %s", text)
	}
	if result["ok"] != true {
		t.Errorf("expected ok=true from reload, got: %v", result)
	}
}

func TestConfigureReload_loadsProjectionsFromDisk(t *testing.T) {
	dir := t.TempDir()
	projDir := filepath.Join(dir, "projections")
	os.MkdirAll(projDir, 0700)
	os.WriteFile(filepath.Join(projDir, "myserver.yaml"), []byte("search:\n  string_limit: 50\n"), 0600)
	os.WriteFile(filepath.Join(dir, "servers.yaml"), []byte("servers:\n  - name: myserver\n    command: echo\n"), 0600)

	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	srv := server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))

	resp := serve(t, srv, callTool("config", map[string]any{"action": "reload"}))
	var result map[string]any
	json.Unmarshal([]byte(toolResultText(t, resp)), &result)
	if result["ok"] != true {
		t.Errorf("expected ok=true from reload, got: %v", result)
	}
}

func TestConfigureAddServer_noConfig(t *testing.T) {
	srv := newTestServer(t)
	resp := serve(t, srv, callTool("config", map[string]any{"action": "add_server"}))
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true when config omitted, got: %v", result)
	}
}

func newServerAllowPrivate(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	cfg.DangerousAllowPrivateURLs = true
	return server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestConfigureAddServer_viaHTTP(t *testing.T) {
	mcp := newMCPTestServer(t, []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	})
	srv := newServerAllowPrivate(t)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "add_server",
		"config": map[string]any{"name": "dynamic", "transport": "http", "url": mcp.URL},
	}))
	var result map[string]any
	json.Unmarshal([]byte(toolResultText(t, resp)), &result)
	if result["ok"] != true || result["server"] != "dynamic" {
		t.Errorf("expected ok add_server, got: %v", result)
	}

	listText := toolResultText(t, serve(t, srv, callTool("list", map[string]any{})))
	var tools []any
	json.Unmarshal([]byte(listText), &tools)
	if len(tools) != 1 {
		t.Errorf("expected 1 tool after add_server, got %d: %s", len(tools), listText)
	}
}

func fakeProtectedConn() (*transport.FakeConnection, *config.PermissionsConfig) {
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "deleteAll", Description: "delete everything", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"deleted"}]}`),
		},
	}
	return fake, &config.PermissionsConfig{Protected: []string{"deleteAll"}}
}

func TestExecuteProtected_callsProtectedTool(t *testing.T) {
	srv := newTestServer(t)
	fake, perm := fakeProtectedConn()
	srv.AddConnection(t.Context(), config.ServerConfig{Name: "db", Permissions: perm}, fake)

	resp := serve(t, srv, callTool("perm_call", map[string]any{
		"server": "db", "tool": "deleteAll", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("expected JSON envelope: %s", text)
	}
	if env["ok"] != true {
		t.Errorf("expected ok=true for valid protected call, got: %v", env)
	}
}

func newReadOnlyConfigServer(t *testing.T) *server.Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Skip("cannot set read-only dir:", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0700) }) //nolint:errcheck
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	return server.NewWithConfigDir(cfg, dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSetProjection_persistenceFailureReturnsError(t *testing.T) {
	srv := newReadOnlyConfigServer(t)
	fake := fakeConn("myTool")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"secret\":\"hidden\"}"}]}`)
	srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, fake)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "myTool",
		"projection": map[string]any{"exclude_always": []string{"secret"}},
	}))
	result, _ := resp["result"].(map[string]any)
	if result == nil || result["isError"] != true {
		t.Errorf("expected isError=true when persistence fails, got: %v", resp)
	}

	execResp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "myTool"}))
	if text := toolResultText(t, execResp); !strings.Contains(text, "secret") {
		t.Error("projection should be rolled back: secret should still appear in response")
	}
}

func TestToolsList_returnsProxySchemas(t *testing.T) {
	srv := newTestServer(t)
	resp := serve(t, srv, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n"))

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got: %v", resp)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) < 4 {
		t.Errorf("expected at least 4 proxy tools, got: %v", result)
	}
}
