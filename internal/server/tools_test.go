//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestUnknownTool_Returns32602(t *testing.T) {
	srv := newTestServer(t)
	resp := serve(t, srv, callTool("nonexistent", map[string]any{}))
	if resp["error"] == nil {
		t.Fatalf("expected JSON-RPC error, got: %v", resp)
	}
	errObj, _ := resp["error"].(map[string]any)
	if code, _ := errObj["code"].(float64); code != -32602 {
		t.Errorf("expected code -32602, got %v", errObj["code"])
	}
}

func TestKnownToolBadArgs_ReturnsIsError(t *testing.T) {
	srv := newTestServer(t)
	fake := fakeConn("ping")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"pong"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "ping",
	}))
	if resp["error"] != nil {
		t.Fatalf("expected tool result, not RPC error: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Error("expected content in result")
	}
}

// TestToolsCall_executionErrorIsIsError verifies that upstream execution errors are returned
// as isError:true in the tool result, NOT as a JSON-RPC error object.
// "Tool errors SHOULD be reported in tool results with isError:true"
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L244
func TestToolsCall_executionErrorIsIsError(t *testing.T) {
	srv := newTestServer(t)
	fake := fakeConnWithError("errTool")
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "errTool", "params": map[string]any{},
	}))
	if resp["error"] != nil {
		t.Errorf("tool execution error must NOT be a JSON-RPC error, got: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("tool execution error must have isError:true, got: %v", result)
	}
	if content, _ := result["content"].([]any); len(content) == 0 {
		t.Errorf("error result must have content array, got: %v", result)
	}
}

// TestToolsListChanged_notificationShape verifies the tools/list_changed notification shape:
// jsonrpc:2.0, no id field, correct method name.
// "Servers that declared listChanged SHOULD send a notification when tool set changes."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L139
func TestToolsListChanged_notificationShape(t *testing.T) {
	srv := newTestServer(t)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "pre"}, fakeConn("existing"))
	msgs := serveAll(t, srv,
		notification(transport.NotificationInitialized, nil),
		callTool("config", map[string]any{"action": "remove_server", "server": "pre"}),
	)
	var found map[string]any
	for _, m := range msgs {
		if m["method"] == transport.NotificationToolsChanged {
			found = m
			break
		}
	}
	if found == nil {
		t.Fatal("tools/list_changed notification not sent after remove_server")
	}
	t.Run("jsonrpc 2.0", func(t *testing.T) {
		if v, _ := found["jsonrpc"].(string); v != "2.0" {
			t.Errorf("notification jsonrpc = %q, want \"2.0\"", v)
		}
	})
	t.Run("no id", func(t *testing.T) {
		// Notifications MUST NOT have an id field per JSON-RPC spec.
		// https://www.jsonrpc.org/specification#notification
		if _, hasID := found["id"]; hasID {
			t.Errorf("notification must not have id, got: %v", found["id"])
		}
	})
	t.Run("method name", func(t *testing.T) {
		if m, _ := found["method"].(string); m != "notifications/tools/list_changed" {
			t.Errorf("method = %q, want notifications/tools/list_changed", m)
		}
	})
}
