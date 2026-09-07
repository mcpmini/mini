//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func TestListDirectoryNonEmpty(t *testing.T) {
	fake := fakeConn("list_directory")
	fake.RespondWith(map[string]any{"entries": []string{"alpha.txt", "beta.go"}})
	srv := newTestServer(t)
	addTestConnection(t, srv, config.ServerConfig{Name: "fs"}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "list_directory", "params": map[string]any{"path": "/dir"},
	}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["error"] != nil {
		t.Fatalf("list_directory failed: %v", env)
	}
	if env["data"] == nil {
		t.Errorf("expected non-empty data, got: %v", env["data"])
	}
}

func TestMiniFormatWithUpstream(t *testing.T) {
	fake := fakeConn("list_directory")
	fake.RespondWith(map[string]any{"entries": []string{"a.txt", "b.txt", "c.go"}})
	srv := newTestServer(t)
	addTestConnection(t, srv, config.ServerConfig{Name: "fs"}, fake)

	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "fs", "tool": "list_directory",
		"projection": map[string]any{"format": "mini"},
	}))
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "list_directory", "params": map[string]any{"path": "/dir"},
	}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Fatalf("expected mini format, got JSON: %s", text)
	}
	if !strings.Contains(text, "[fs.list_directory]") {
		t.Errorf("missing header line in mini format output: %s", text)
	}
}

func TestReadFileTruncation(t *testing.T) {
	fake := fakeConn("read_file")
	fake.RespondWith(strings.Repeat("The quick brown fox jumps over the lazy dog. ", 50))

	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DefaultStringLimit = 100
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)
	addTestConnection(t, srv, config.ServerConfig{Name: "fs"}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "read_file", "params": map[string]any{"path": "/big.txt"},
	}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["error"] != nil {
		t.Fatalf("read_file failed: %v", env)
	}
	truncated, _ := env["truncated"].([]any)
	if len(truncated) == 0 {
		t.Errorf("expected truncated entries in envelope after string limit applied, got: %v", env)
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
	fake1 := fakeConn("list_directory")
	fake1.RespondWith(map[string]any{"entries": []string{"from_server1.txt"}})
	fake2 := fakeConn("list_directory")
	fake2.RespondWith(map[string]any{"entries": []string{"from_server2.txt"}})
	srv := newTestServer(t)
	addTestConnection(t, srv, config.ServerConfig{Name: "fs1"}, fake1)
	addTestConnection(t, srv, config.ServerConfig{Name: "fs2"}, fake2)

	var tools []map[string]any
	json.Unmarshal([]byte(toolResultText(t, serve(t, srv, callTool("list", map[string]any{})))), &tools) //nolint:errcheck
	if countToolsByPrefix(tools, "fs1.") == 0 || countToolsByPrefix(tools, "fs2.") == 0 {
		t.Errorf("expected tools from both servers, got fs1=%d fs2=%d",
			countToolsByPrefix(tools, "fs1."), countToolsByPrefix(tools, "fs2."))
	}
	resp1 := serve(t, srv, callTool("call", map[string]any{
		"server": "fs1", "tool": "list_directory", "params": map[string]any{"path": "/dir"},
	}))
	if env1 := parseEnvelope(t, toolResultText(t, resp1)); env1["error"] != nil {
		t.Errorf("expected ok=true from fs1: %v", env1)
	}
}

func assertAddServer(t *testing.T, srv *server.Server) {
	t.Helper()
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "add_server",
		"config": map[string]any{"name": "dynamic_echo", "command": echomcpBin},
	}))
	text := toolResultText(t, resp)
	var result map[string]any
	json.Unmarshal([]byte(text), &result) //nolint:errcheck
	if result["error"] != nil {
		t.Fatalf("add_server failed: %s", text)
	}
	if text2 := toolResultText(t, serve(t, srv, callTool("list", map[string]any{}))); !strings.Contains(text2, "dynamic_echo") {
		t.Errorf("expected dynamic_echo tools after add_server: %s", text2)
	}
}

func assertRemoveServer(t *testing.T, srv *server.Server) {
	t.Helper()
	resp := serve(t, srv, callTool("config", map[string]any{"action": "remove_server", "server": "dynamic_echo"}))
	var result map[string]any
	json.Unmarshal([]byte(toolResultText(t, resp)), &result) //nolint:errcheck
	if result["error"] != nil {
		t.Fatalf("remove_server failed: %v", result)
	}
	if text := toolResultText(t, serve(t, srv, callTool("list", map[string]any{}))); strings.Contains(text, "dynamic_echo") {
		t.Errorf("dynamic_echo still present after remove: %s", text)
	}
}

func TestAddRemoveServer(t *testing.T) {
	if echomcpBin == "" {
		t.Fatal("ECHOMCP_BIN not set; run check.sh or: go build -o /tmp/echomcp ./cmd/echomcp && ECHOMCP_BIN=/tmp/echomcp go test ...")
	}
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.DangerousAllowRuntimeStdio = true
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)
	assertAddServer(t, srv)
	assertRemoveServer(t, srv)
}

func TestStdioEnvPassthrough(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)

	sc := config.ServerConfig{
		Name:    "envtest",
		Command: os.Args[0],
		Env:     []string{"MINI_HELPER_PROCESS=1", "MINI_TEST_VAR=hello_from_mini"},
	}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("AddUpstream: %v", err)
	}
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "envtest", "tool": "get_env", "params": map[string]any{},
	}))
	if text := toolResultText(t, resp); !strings.Contains(text, "hello_from_mini") {
		t.Errorf("MINI_TEST_VAR not in subprocess response, got: %s", text)
	}
}

func TestAddUpstream_handshakeTimeoutSkipsHungStdioSubprocess(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(srv.Close)

	sc := config.ServerConfig{
		Name:             "hungupstream",
		Command:          "sleep",
		Args:             []string{"30"},
		HandshakeTimeout: "100ms",
	}
	start := time.Now()
	err := srv.AddUpstream(context.Background(), sc)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected AddUpstream to fail for an upstream that never answers initialize")
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("AddUpstream did not respect handshake_timeout, took %v", elapsed)
	}
}
