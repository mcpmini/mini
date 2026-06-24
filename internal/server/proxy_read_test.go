//go:build test

package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func TestProxy_MiniRead_ReadsFile(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serveProxy(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_item",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp1 := serveProxy(t, srv, callTool("svc__get_item", map[string]any{}))
	text1 := toolResultText(t, resp1)
	t.Logf("initial response: %s", text1)

	if !strings.Contains(text1, "File:") {
		t.Fatal("expected file to be written — elision of 'secret' should trigger raw file write")
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

	resp2 := serveProxy(t, srv, callTool("read", map[string]any{"path": filePath}))
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

	resp := serveProxy(t, srv, callTool("read", map[string]any{"path": "/etc/passwd"}))
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

	resp := serveProxy(t, srv, callTool("read", map[string]any{}))
	if resp["error"] != nil {
		return // RPC-level error is correct
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Error("expected error when path is empty")
	}
}

func TestProxy_UnknownTool_ReturnsError(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	resp := serveProxy(t, srv, callTool("nonexistent__tool", map[string]any{}))
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

	resp := serveProxy(t, srv, callTool("notaproxytool", map[string]any{}))
	if resp["error"] == nil {
		result, ok := resp["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Errorf("expected error for malformed proxy tool name, got: %v", resp)
		}
	}
}
