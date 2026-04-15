//go:build test

package server_test

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

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
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
	if env["error"] != nil {
		t.Fatalf("list_directory failed: %v", env)
	}
	if env["data"] == nil || env["data"] == "" {
		t.Errorf("expected non-empty data, got: %v", env["data"])
	}
}

func TestMiniFormatWithRealFS(t *testing.T) {
	dir := realPath(t, t.TempDir())
	for _, name := range []string{"a.txt", "b.txt", "c.go"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644)
	}
	srv := fsServer(t, dir)
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "fs", "tool": "list_directory",
		"projection": map[string]any{"format": "mini"},
	}))
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "list_directory", "params": map[string]any{"path": dir},
	}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Fatalf("expected mini format, got JSON: %s", text)
	}
	if !strings.Contains(text, "[fs.list_directory]") {
		t.Errorf("missing header line: %s", text)
	}
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
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not available")
	}
	sc := config.ServerConfig{Name: "fs", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", dir}}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("connect: %v", err)
	}

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "read_file", "params": map[string]any{"path": filepath.Join(dir, "big.txt")},
	}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["error"] != nil {
		t.Fatalf("read_file failed: %v", env)
	}
	truncated, _ := env["truncated"].(map[string]any)
	if len(truncated) == 0 {
		t.Errorf("expected truncated map in envelope after string limit applied, got: %v", env)
	}
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
	if env1 := parseEnvelope(t, toolResultText(t, resp1)); env1["error"] != nil {
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
	if result["error"] != nil {
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
	if result["error"] != nil {
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
