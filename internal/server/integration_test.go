//go:build live

package server_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
)

func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return real
}

func setupFSServer(t *testing.T, allowedDir string) (*server.Server, context.CancelFunc) {
	t.Helper()
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cfg := config.DefaultConfig()
	cfg.ResponseDir = allowedDir
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := server.New(server.Params{Config: cfg, ConfigDir: t.TempDir(), Logger: logger})
	sc := config.ServerConfig{
		Name:    "fs",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", allowedDir},
	}
	if err := srv.AddUpstream(ctx, sc); err != nil {
		t.Fatalf("connect to filesystem MCP: %v", err)
	}
	return srv, cancel
}

func fsTestDiscover(t *testing.T, srv *server.Server) {
	t.Helper()
	text := toolListJSON(t, serve(t, srv, rpc("tools/list", nil)))
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
	parseEnvelope(t, successText(t, serve(t, srv, callTool("fs__list_directory", map[string]any{"path": dir}))))
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
	t.Run("configure status shows fs server", func(t *testing.T) { fsTestStatusShowsFS(t, srv) })
}

func newWritableFSServer(t *testing.T) (*server.Server, string) {
	t.Helper()
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := newTestServer(t, server.Params{})
	t.Cleanup(srv.Close)
	tmpDir := realPath(t, t.TempDir())
	sc := config.ServerConfig{
		Name:    "fs",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", tmpDir},
	}
	if err := srv.AddUpstream(ctx, sc); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return srv, tmpDir
}

func TestWithRealFilesystemMCP_WriteFile(t *testing.T) {
	srv, tmpDir := newWritableFSServer(t)
	testFile := filepath.Join(tmpDir, "hello.txt")
	resp := serve(t, srv, callTool("fs__write_file", map[string]any{"path": testFile, "content": "hello from mini"}))
	successText(t, resp)
	testutil.ReadFile(t, testFile)
}
