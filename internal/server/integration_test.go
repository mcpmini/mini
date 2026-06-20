//go:build live

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

func setupFSServer(t *testing.T, allowedDir string) (*server.Server, context.CancelFunc) {
	t.Helper()
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cfg := config.DefaultConfig()
	cfg.ResponseDir = allowedDir
	srv := server.New(cfg, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	sc := config.ServerConfig{
		Name: "fs", Command: "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-filesystem", allowedDir},
		Permissions: &config.PermissionsConfig{Protected: []string{"write_file", "create_directory", "move_file", "delete_file"}},
	}
	if err := srv.AddUpstream(ctx, sc); err != nil {
		t.Fatalf("connect to filesystem MCP: %v", err)
	}
	return srv, cancel
}

func fsTestDiscover(t *testing.T, srv *server.Server) {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("list", map[string]any{})))
	var tools []map[string]any
	if err := json.Unmarshal([]byte(text), &tools); err != nil {
		t.Fatalf("discover not JSON: %v\n%s", err, text)
	}
	if len(tools) == 0 {
		t.Error("expected filesystem tools, got none")
	}
}

func fsTestListDir(t *testing.T, srv *server.Server, dir string) {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "list_directory", "params": map[string]any{"path": dir},
	})))
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("result not JSON: %v\n%s", err, text)
	}
	if env["error"] != nil {
		t.Errorf("expected ok=true, got: %v\nfull: %s", env["ok"], text)
	}
}

func fsTestWriteFileBlocked(t *testing.T, srv *server.Server) {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "write_file",
		"params": map[string]any{"path": "/tmp/test.txt", "content": "hello"},
	})))
	if !strings.Contains(text, "perm_call") {
		t.Errorf("expected perm_call error, got: %s", text)
	}
}

func fsTestDetailSchema(t *testing.T, srv *server.Server) {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("list", map[string]any{
		"tool": "fs.list_directory", "detail": true,
	})))
	var detail map[string]any
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("detail not JSON: %v", err)
	}
	if detail["inputSchema"] == nil {
		t.Error("expected inputSchema in detail")
	}
}

func fsTestStatusShowsFS(t *testing.T, srv *server.Server) {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "status"})))
	var status map[string]any
	json.Unmarshal([]byte(text), &status) //nolint:errcheck
	if servers, _ := status["servers"].(map[string]any); servers["fs"] == nil {
		t.Errorf("expected fs in servers: %s", text)
	}
}

func TestWithRealFilesystemMCP(t *testing.T) {
	allowedDir := realPath(t, t.TempDir())
	srv, cancel := setupFSServer(t, allowedDir)
	defer cancel()
	defer srv.Close()
	t.Run("discover lists filesystem tools", func(t *testing.T) { fsTestDiscover(t, srv) })
	t.Run("execute list_directory returns envelope", func(t *testing.T) { fsTestListDir(t, srv, allowedDir) })
	t.Run("execute write_file rejected without perm_call", func(t *testing.T) { fsTestWriteFileBlocked(t, srv) })
	t.Run("discover detail returns schema", func(t *testing.T) { fsTestDetailSchema(t, srv) })
	t.Run("configure status shows fs server", func(t *testing.T) { fsTestStatusShowsFS(t, srv) })
}

func newProtectedFSServer(t *testing.T) (*server.Server, string) {
	t.Helper()
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)
	tmpDir := realPath(t, t.TempDir())
	sc := config.ServerConfig{
		Name: "fs", Command: "npx",
		Args:        []string{"-y", "@modelcontextprotocol/server-filesystem", tmpDir},
		Permissions: &config.PermissionsConfig{Protected: []string{"write_file", "create_directory", "delete_file", "move_file"}},
	}
	if err := srv.AddUpstream(ctx, sc); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return srv, tmpDir
}

func TestWithRealFilesystemMCP_WriteProtected(t *testing.T) {
	srv, tmpDir := newProtectedFSServer(t)
	testFile := filepath.Join(tmpDir, "hello.txt")
	resp := serve(t, srv, callTool("perm_call", map[string]any{
		"server": "fs", "tool": "write_file",
		"params": map[string]any{"path": testFile, "content": "hello from mini"},
	}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["error"] != nil {
		t.Errorf("write_file failed: %v", env)
	}
	if _, err := os.ReadFile(testFile); err != nil {
		t.Fatalf("file not written: %v", err)
	}
}


