//go:build test

package server_test

// Critical end-to-end tests covering real edge cases and failure modes.
// All tests skip if npx is unavailable.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func newNpxServer(t *testing.T) *server.Server {
	t.Helper()
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not available")
	}
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)
	return srv
}

func addFSUpstream(t *testing.T, srv *server.Server, name, dir string) {
	t.Helper()
	sc := config.ServerConfig{Name: name, Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", dir}}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("connect %s: %v", name, err)
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

func fsServer(t *testing.T, dir string) *server.Server {
	t.Helper()
	srv := newNpxServer(t)
	sc := config.ServerConfig{
		Name: "fs", Command: "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-filesystem", dir},
		Permissions: &config.PermissionsConfig{Protected: []string{"write_file", "create_directory", "move_file", "delete_file", "edit_file"}},
	}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return srv
}

func TestListDirectoryNonEmpty(t *testing.T) {
	dir := realPath(t, t.TempDir())
	os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("hello"), 0644)
	os.WriteFile(filepath.Join(dir, "beta.go"), []byte("package main"), 0644)
	srv := fsServer(t, dir)
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "list_directory", "params": map[string]any{"path": dir},
	}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["ok"] != true {
		t.Fatalf("list_directory failed: %v", env)
	}
	if env["data"] == nil || env["data"] == "" {
		t.Errorf("expected non-empty data, got: %v", env["data"])
	}
}

func TestLinesFormatWithRealFS(t *testing.T) {
	dir := realPath(t, t.TempDir())
	for _, name := range []string{"a.txt", "b.txt", "c.go"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644)
	}
	srv := fsServer(t, dir)
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "fs", "tool": "list_directory",
		"projection": map[string]any{"format": "lines"},
	}))
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "list_directory", "params": map[string]any{"path": dir},
	}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Fatalf("expected lines format, got JSON: %s", text)
	}
	if !strings.Contains(text, "[fs.list_directory]") {
		t.Errorf("missing header line: %s", text)
	}
}

func extractSummaryString(env map[string]any) string {
	switch s := env["data"].(type) {
	case string:
		return s
	case map[string]any:
		c, _ := s["contents"].(string)
		return c
	}
	return ""
}

func TestReadFileTruncation(t *testing.T) {
	dir := realPath(t, t.TempDir())
	content := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 50)
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(content), 0644)

	// Use a global string limit to verify truncation is applied to plain-string responses.
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	cfg.DefaultStringLimit = 100
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)
	sc := config.ServerConfig{Name: "fs", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", dir}}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("connect: %v", err)
	}

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "read_file", "params": map[string]any{"path": filepath.Join(dir, "big.txt")},
	}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["ok"] != true {
		t.Fatalf("read_file failed: %v", env)
	}
	if !strings.Contains(extractSummaryString(env), "<trnc") {
		t.Errorf("expected <trnc marker in truncated content")
	}
}

func assertExcluded(t *testing.T, obj map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if obj[k] != nil {
			t.Errorf("field %q should be excluded, got: %v", k, obj[k])
		}
	}
}

func execGetFileInfo(t *testing.T, srv *server.Server) map[string]any {
	t.Helper()
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "get_file_info", "params": map[string]any{},
	}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["ok"] != true {
		t.Fatalf("get_file_info failed: %v", env)
	}
	summary, _ := env["data"].(map[string]any)
	if summary == nil {
		t.Fatalf("nil summary: %v", env)
	}
	return summary
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

func countToolsByPrefix(tools []map[string]any, prefix string) int {
	n := 0
	for _, tool := range tools {
		if name, _ := tool["name"].(string); strings.HasPrefix(name, prefix) {
			n++
		}
	}
	return n
}

func TestMultipleServers(t *testing.T) {
	srv := newNpxServer(t)
	dir1, dir2 := realPath(t, t.TempDir()), realPath(t, t.TempDir())
	os.WriteFile(filepath.Join(dir1, "from_server1.txt"), []byte("s1"), 0644)
	os.WriteFile(filepath.Join(dir2, "from_server2.txt"), []byte("s2"), 0644)
	addFSUpstream(t, srv, "fs1", dir1)
	addFSUpstream(t, srv, "fs2", dir2)
	var tools []map[string]any
	json.Unmarshal([]byte(toolResultText(t, serve(t, srv, callTool("list", map[string]any{})))), &tools)
	if countToolsByPrefix(tools, "fs1.") == 0 || countToolsByPrefix(tools, "fs2.") == 0 {
		t.Errorf("expected tools from both servers, got fs1=%d fs2=%d",
			countToolsByPrefix(tools, "fs1."), countToolsByPrefix(tools, "fs2."))
	}
	resp1 := serve(t, srv, callTool("call", map[string]any{
		"server": "fs1", "tool": "list_directory", "params": map[string]any{"path": dir1},
	}))
	if env1 := parseEnvelope(t, toolResultText(t, resp1)); env1["ok"] != true {
		t.Errorf("expected ok=true from fs1: %v", env1)
	}
}

func assertAddServer(t *testing.T, srv *server.Server, dir string) {
	t.Helper()
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "add_server",
		"config": map[string]any{"name": "dynamic_fs", "command": "npx",
			"args": []string{"-y", "@modelcontextprotocol/server-filesystem", dir}},
	}))
	text := toolResultText(t, resp)
	var result map[string]any
	json.Unmarshal([]byte(text), &result)
	if result["ok"] != true {
		t.Fatalf("add_server failed: %s", text)
	}
	if text2 := toolResultText(t, serve(t, srv, callTool("list", map[string]any{}))); !strings.Contains(text2, "dynamic_fs") {
		t.Errorf("expected dynamic_fs tools after add_server: %s", text2)
	}
}

func assertRemoveServer(t *testing.T, srv *server.Server) {
	t.Helper()
	resp := serve(t, srv, callTool("config", map[string]any{"action": "remove_server", "server": "dynamic_fs"}))
	var result map[string]any
	json.Unmarshal([]byte(toolResultText(t, resp)), &result)
	if result["ok"] != true {
		t.Fatalf("remove_server failed: %v", result)
	}
	if text := toolResultText(t, serve(t, srv, callTool("list", map[string]any{}))); strings.Contains(text, "dynamic_fs") {
		t.Errorf("dynamic_fs still present after remove: %s", text)
	}
}

func TestAddRemoveServer(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not available")
	}
	cfg := config.DefaultConfig()
	cfg.ResponseDir, cfg.InlineThreshold, cfg.DangerousAllowRuntimeStdio = t.TempDir(), 10000, true
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)
	dir := realPath(t, t.TempDir())
	assertAddServer(t, srv, dir)
	assertRemoveServer(t, srv)
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

func assertSlimShorterWithTrnc(t *testing.T, textFull, textSlim string) {
	t.Helper()
	if len(textSlim) >= len(textFull) {
		t.Errorf("slim (%d bytes) should be shorter than full (%d bytes)", len(textSlim), len(textFull))
	}
	var slimEnv map[string]any
	json.Unmarshal([]byte(textSlim), &slimEnv)
	slimData, _ := json.Marshal(slimEnv["data"])
	if !strings.Contains(string(slimData), "trnc") {
		t.Errorf("expected trnc marker in slim data: %s", slimData)
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
	assertSlimShorterWithTrnc(t, textFull, execListItems())
}

