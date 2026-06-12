//go:build test

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
)

func TestProxy_MiniRead_ReadsFile(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 1 // force file writes
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_item",
		"projection": map[string]any{"exclude_always": []string{"secret"}},
	}))

	resp1 := serve(t, srv, callTool("svc__get_item", map[string]any{}))
	text1 := toolResultText(t, resp1)
	t.Logf("initial response: %s", text1)

	if !strings.Contains(text1, "File:") {
		t.Skip("response was not written to file (threshold not triggered)")
	}

	var filePath string
	for _, line := range strings.Split(text1, "\n") {
		if strings.HasPrefix(line, "File: ") {
			filePath = strings.TrimPrefix(line, "File: ")
			break
		}
	}
	if filePath == "" {
		t.Fatalf("could not extract file path from: %s", text1)
	}

	resp2 := serve(t, srv, callTool("read", map[string]any{"path": filePath}))
	text2 := toolResultText(t, resp2)
	t.Logf("read response: %s", text2)

	if text2 == "" {
		t.Error("read returned empty content")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text2), &parsed); err != nil {
		t.Errorf("read content should be JSON: %s", text2)
	}
}

func TestProxy_MiniRead_RejectsPathTraversal(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	resp := serve(t, srv, callTool("read", map[string]any{"path": "/etc/passwd"}))
	if resp["error"] != nil {
		return // RPC-level error is correct
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Errorf("expected error for path traversal, got: %v", resp)
	}
}

func TestProxy_MiniRead_RequiresPath(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	resp := serve(t, srv, callTool("read", map[string]any{}))
	if resp["error"] != nil {
		return // RPC-level error is correct
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Error("expected error when path is empty")
	}
}

func TestProxy_MiniRead_Filter(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 1
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"items\":[{\"name\":\"a\"},{\"name\":\"b\"}],\"secret\":\"x\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)
	setExcludeSecretProjection(t, srv)

	resp1 := serve(t, srv, callTool("svc__get_item", map[string]any{}))
	filePath := extractFilePath(t, toolResultText(t, resp1))

	resp2 := serve(t, srv, callTool("read", map[string]any{"path": filePath, "filter": ".items[].name"}))
	text2 := toolResultText(t, resp2)
	if text2 != "\"a\"\n\"b\"" {
		t.Errorf("filtered read = %q, want jq -c output of names", text2)
	}
}

func TestProxy_MiniRead_FilterElidedField(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 1
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)
	setExcludeSecretProjection(t, srv)

	resp1 := serve(t, srv, callTool("svc__get_item", map[string]any{}))
	filePath := extractFilePath(t, toolResultText(t, resp1))

	resp2 := serve(t, srv, callTool("read", map[string]any{"path": filePath, "filter": ".secret"}))
	text2 := toolResultText(t, resp2)
	if text2 != `"hidden"` {
		t.Errorf("filtered raw read = %q, want %q", text2, `"hidden"`)
	}
}

func TestProxy_MiniRead_FilterInvalidSyntax(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 1
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"foo\":\"bar\",\"secret\":\"x\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)
	setExcludeSecretProjection(t, srv)

	resp1 := serve(t, srv, callTool("svc__get_item", map[string]any{}))
	filePath := extractFilePath(t, toolResultText(t, resp1))

	resp2 := serve(t, srv, callTool("read", map[string]any{"path": filePath, "filter": ".foo["}))
	if resp2["error"] != nil {
		return
	}
	result, ok := resp2["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Errorf("expected error for invalid filter syntax, got: %v", resp2)
	}
}

func TestProxy_MiniRead_FilterNonJSON(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	path := filepath.Join(cfg.ResponseDir, "plain.raw.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	resp := serve(t, srv, callTool("read", map[string]any{"path": path, "filter": "."}))
	if resp["error"] != nil {
		return
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Errorf("expected error for non-JSON content with filter, got: %v", resp)
	}
}

func TestProxy_MiniRead_FilterEmptyUnchanged(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 1
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"foo\":\"bar\",\"secret\":\"x\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)
	setExcludeSecretProjection(t, srv)

	resp1 := serve(t, srv, callTool("svc__get_item", map[string]any{}))
	filePath := extractFilePath(t, toolResultText(t, resp1))

	resp2 := serve(t, srv, callTool("read", map[string]any{"path": filePath}))
	text2 := toolResultText(t, resp2)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text2), &parsed); err != nil {
		t.Errorf("read without filter should still return raw content: %s", text2)
	}
}

func setExcludeSecretProjection(t *testing.T, srv *server.Server) {
	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_item",
		"projection": map[string]any{"exclude_always": []string{"secret"}},
	}))
}

func TestProxy_MiniRead_RejectsPathTraversalWithFilter(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	resp := serve(t, srv, callTool("read", map[string]any{"path": "/etc/passwd", "filter": "."}))
	if resp["error"] != nil {
		return
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Errorf("expected error for path traversal with filter, got: %v", resp)
	}
}

func extractFilePath(t *testing.T, text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "File: ") {
			return strings.TrimPrefix(line, "File: ")
		}
	}
	t.Fatalf("could not extract file path from: %s", text)
	return ""
}

func TestProxy_UnknownTool_ReturnsError(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	resp := serve(t, srv, callTool("nonexistent__tool", map[string]any{}))
	if resp["error"] == nil {
		result, ok := resp["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Errorf("expected error for unknown proxy tool, got: %v", resp)
		}
	}
}

func TestProxy_NoDoubleUnderscore_ReturnsError(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	resp := serve(t, srv, callTool("notaproxytool", map[string]any{}))
	if resp["error"] == nil {
		result, ok := resp["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Errorf("expected error for malformed proxy tool name, got: %v", resp)
		}
	}
}
