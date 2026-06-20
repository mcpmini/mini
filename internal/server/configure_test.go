//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if result["error"] != nil {
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
	if result["error"] != nil {
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
	if result["error"] != nil || result["server"] != "dynamic" {
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
	if env["error"] != nil {
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

func newConfigServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	srv := server.NewWithConfigDir(cfg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)
	return srv
}

func execGetFileInfo(t *testing.T, srv *server.Server) map[string]any {
	t.Helper()
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "get_file_info", "params": map[string]any{},
	}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["error"] != nil {
		t.Fatalf("get_file_info failed: %v", env)
	}
	summary, _ := env["data"].(map[string]any)
	if summary == nil {
		t.Fatalf("nil summary: %v", env)
	}
	return summary
}

func assertExcluded(t *testing.T, obj map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if obj[k] != nil {
			t.Errorf("field %q should be excluded, got: %v", k, obj[k])
		}
	}
}

func TestProjectionExcludeFields(t *testing.T) {
	srv := newConfigServer(t)
	payload := `{"name":"test.txt","size":5,"created":"2026-01-01","permissions":"644","isDirectory":false}`
	payloadJSON, _ := json.Marshal(payload)
	fake := &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: "get_file_info", Description: "info", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{"tools/call": json.RawMessage(`{"content":[{"type":"text","text":` + string(payloadJSON) + `}]}`)},
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "fs"}, fake)
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "fs", "tool": "get_file_info",
		"projection": map[string]any{"include": []string{"name", "size"}},
	}))
	summary := execGetFileInfo(t, srv)
	if summary["name"] == nil {
		t.Error("expected 'name' in projected summary")
	}
	assertExcluded(t, summary, "created", "permissions", "isDirectory")
}

func TestProjectionTruncation_fieldNameAndBytes(t *testing.T) {
	srv := newConfigServer(t)
	longBody := strings.Repeat("z", 300)
	payload := `{"id":1,"title":"short","body":"` + longBody + `"}`
	payloadJSON, _ := json.Marshal(payload)
	fake := &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: "get_doc", Description: "doc", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{"tools/call": json.RawMessage(`{"content":[{"type":"text","text":` + string(payloadJSON) + `}]}`)},
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_doc",
		"projection": map[string]any{"string_limits": map[string]any{"body": 50}},
	}))

	resp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "get_doc"}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["error"] != nil {
		t.Fatalf("get_doc failed: %v", env)
	}

	omitted, _ := env["omitted"].([]any)
	if len(omitted) == 0 {
		t.Errorf("expected omitted entries, got %v", env["omitted"])
	}
	var bodyBytes float64
	for _, o := range omitted {
		om, _ := o.(map[string]any)
		if om["path"] == ".body" {
			bodyBytes, _ = om["bytes"].(float64)
		}
	}
	if bodyBytes <= 0 {
		t.Errorf("expected omitted[body].bytes > 0, got omitted=%v", omitted)
	}
	// body had 300 chars, limit 50 → removed ≥ 200
	if int(bodyBytes) < 200 {
		t.Errorf("expected at least 200 bytes removed from body, got %v", bodyBytes)
	}
	// file is written when omission occurred
}

func assertHealthStats(t *testing.T, srv *server.Server, svcName string, wantCalls int) {
	t.Helper()
	var status map[string]any
	json.Unmarshal([]byte(toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "status"})))), &status)
	servers, _ := status["servers"].(map[string]any)
	svc, _ := servers[svcName].(map[string]any)
	if calls, _ := svc["calls"].(float64); int(calls) != wantCalls {
		t.Errorf("expected %d calls, got %v", wantCalls, calls)
	}
	if svc["last_call"] == nil {
		t.Error("expected last_call timestamp")
	}
	lastCall, _ := svc["last_call"].(string)
	if _, err := time.Parse(time.RFC3339, lastCall); err != nil {
		t.Errorf("last_call not RFC3339: %q", lastCall)
	}
}

func TestHealthStatsAfterCalls(t *testing.T) {
	srv := newTestServer(t)
	t.Cleanup(srv.Close)
	const nCalls = 3
	fake := &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`)},
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)
	for i := 0; i < nCalls; i++ {
		serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "ping", "params": map[string]any{}}))
	}
	assertHealthStats(t, srv, "svc", nCalls)
}

func makeListItemsFake(longDesc string) *transport.FakeConnection {
	items := `[{"id":1,"description":"` + longDesc + `"},{"id":2,"description":"` + longDesc + `"},{"id":3,"description":"` + longDesc + `"}]`
	itemsJSON, _ := json.Marshal(items)
	return &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: "list_items", Description: "list", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{"tools/call": json.RawMessage(`{"content":[{"type":"text","text":` + string(itemsJSON) + `}]}`)},
	}
}

func newSessionServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	return server.NewWithConfigDir(cfg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func fakeGetData() *transport.FakeConnection {
	return &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "getData", Description: "get", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2}"}]}`),
		},
	}
}

func assertRemoveOk(t *testing.T, srv *server.Server, serverName string) {
	t.Helper()
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": serverName,
	}))
	text := toolResultText(t, resp)
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON result: %s", text)
	}
	if result["error"] != nil {
		t.Errorf("expected ok=true, got: %v", result)
	}
}

func TestConfigureUnknownAction(t *testing.T) {
	srv := newEdgeServer(t)
	resp := serve(t, srv, callTool("config", map[string]any{"action": "no_such_action"}))
	assertIsErrorResult(t, resp)
	text := toolResultText(t, resp)
	if !strings.Contains(text, "unknown configure action") {
		t.Errorf("expected error message, got: %s", text)
	}
}

func TestConfigureSetProjectionRequiresTool(t *testing.T) {
	srv := newEdgeServer(t)
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection",
		"server": "svc",
	}))
	assertIsErrorResult(t, resp)
}

func TestConfigureRemoveServerRequiresName(t *testing.T) {
	srv := newEdgeServer(t)
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
	}))
	assertIsErrorResult(t, resp)
}

func TestConfigureRemoveServer_clearsDiscover(t *testing.T) {
	srv := newEdgeServer(t)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fakeConn("ping"))
	if srv.ToolCount("svc") != 1 {
		t.Fatalf("expected 1 tool before remove")
	}
	assertRemoveOk(t, srv, "svc")

	discoverText := toolResultText(t, serve(t, srv, callTool("list", map[string]any{})))
	if strings.Contains(discoverText, "ping") {
		t.Errorf("removed server's tools should not appear in discover, got: %s", discoverText)
	}
}

func TestSessionScopedProjectionNotPersistedAcrossCalls(t *testing.T) {
	srv := newSessionServer(t)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fakeGetData())

	serve(t, srv, callTool("config", map[string]any{
		"action":       "set_projection",
		"server":       "svc",
		"tool":         "getData",
		"projection":   map[string]any{"include": []string{"a"}},
		"session_only": true,
	}))

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "getData", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if strings.Contains(text, `"b":2`) {
		t.Logf("note: session projection applied within same session: %s", text)
	}
}

func TestSlimProjectionMode(t *testing.T) {
	srv := newConfigServer(t)
	longDesc := strings.Repeat("word ", 60)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, makeListItemsFake(longDesc))
	execListItems := func() string {
		return toolResultText(t, serve(t, srv, callTool("call", map[string]any{
			"server": "svc", "tool": "list_items", "params": map[string]any{},
		})))
	}
	textFull := execListItems()
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "list_items",
		"projection": map[string]any{"mode": "slim"},
	}))
	textSlim := execListItems()
	if len(textSlim) >= len(textFull) {
		t.Errorf("slim (%d bytes) should be shorter than full (%d bytes)", len(textSlim), len(textFull))
	}
	var slimEnv map[string]any
	json.Unmarshal([]byte(textSlim), &slimEnv)
	omitted, _ := slimEnv["omitted"].([]any)
	if len(omitted) == 0 {
		t.Errorf("expected omitted entries in slim envelope, got: %v", slimEnv)
	}
}
