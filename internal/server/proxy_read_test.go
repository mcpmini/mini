//go:build test

package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
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

	var env map[string]any
	if err := json.Unmarshal([]byte(text1), &env); err != nil {
		t.Fatalf("expected JSON envelope from projected call, got: %s", text1)
	}
	mini, _ := env["__mini"].(map[string]any)
	if mini == nil {
		t.Fatal("expected __mini key — exclusion of 'secret' should produce projection envelope")
	}
	filePath, _ := mini["file"].(string)
	if filePath == "" {
		t.Fatalf("expected __mini.file to be set, got: %s", text1)
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

func TestProxy_MiniRead_WithFilter(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	conn := fakeConn("get_data")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":99,\"name\":\"widget\",\"secret\":\"x\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serveProxy(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_data",
		"projection": map[string]any{"exclude_always": []string{"secret"}},
	}))

	resp1 := serveProxy(t, srv, callTool("svc__get_data", map[string]any{}))
	text1 := toolResultText(t, resp1)
	var env map[string]any
	_ = json.Unmarshal([]byte(text1), &env)
	filePath, _ := env["__mini"].(map[string]any)["file"].(string)
	if filePath == "" {
		t.Fatalf("expected file path in __mini, got: %s", text1)
	}

	resp2 := serveProxy(t, srv, callTool("read", map[string]any{"path": filePath, "filter": ".name"}))
	text2 := toolResultText(t, resp2)
	if text2 != `"widget"` {
		t.Errorf("filter .name: expected %q, got %q", `"widget"`, text2)
	}
}

func TestProxy_MiniRead_InvalidFilterReturnsError(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	conn := fakeConn("get_data2")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"x\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serveProxy(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_data2",
		"projection": map[string]any{"exclude_always": []string{"secret"}},
	}))

	resp1 := serveProxy(t, srv, callTool("svc__get_data2", map[string]any{}))
	text1 := toolResultText(t, resp1)
	var env map[string]any
	_ = json.Unmarshal([]byte(text1), &env)
	filePath, _ := env["__mini"].(map[string]any)["file"].(string)
	if filePath == "" {
		t.Fatalf("expected file path in __mini, got: %s", text1)
	}

	resp2 := serveProxy(t, srv, callTool("read", map[string]any{"path": filePath, "filter": "!!!invalid_jq!!!"}))
	if resp2["error"] != nil {
		return // RPC-level error is correct
	}
	result, ok := resp2["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Errorf("expected error for invalid jq filter, got: %v", resp2)
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
