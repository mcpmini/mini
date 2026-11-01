//go:build test

package server_test

import (
	"context"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestNotification_toolsChangedAfterAddServer(t *testing.T) {
	mcp := newMCPTestServer(t, []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	})

	cfg := config.DefaultConfig()
	cfg.DangerousAllowPrivateURLs = true
	srv := newTestServer(t, server.Params{Config: cfg})

	msgs := serveAll(t, srv,
		notification("notifications/initialized", nil),
		callTool("config", map[string]any{
			"action": "add_server",
			"config": map[string]any{"name": "dynamic", "transport": "http", "url": mcp.URL},
		}),
	)

	notification := findMethod(msgs, "notifications/tools/list_changed")
	if notification == nil {
		t.Fatalf("expected notifications/tools/list_changed after add_server; got: %v", msgs)
	}
	if params, ok := notification["params"].(map[string]any); ok && params["_meta"] != nil {
		t.Errorf("legacy tools changed notification must be untagged, got: %v", notification)
	}
}

func TestNotification_toolsChangedAfterRemoveServer(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fakeConn("aTool")) //nolint:errcheck

	msgs := serveAll(t, srv,
		notification("notifications/initialized", nil),
		callTool("config", map[string]any{"action": "remove_server", "server": "svc"}),
	)

	if !hasNotification(msgs, "notifications/tools/list_changed") {
		t.Errorf("expected notifications/tools/list_changed after remove_server; got: %v", msgs)
	}
}

func TestNotification_noNotificationForOtherConfigureActions(t *testing.T) {
	msgs := serveAll(t, newTestServer(t, server.Params{}),
		callTool("config", map[string]any{"action": "status"}),
	)

	for _, m := range msgs {
		if m["method"] == "notifications/tools/list_changed" {
			t.Errorf("unexpected tools/list_changed for configure status: %v", m)
		}
	}
}

func findMethod(messages []map[string]any, method string) map[string]any {
	for _, message := range messages {
		if message["method"] == method {
			return message
		}
	}
	return nil
}

// TestToolsListChanged_notificationShape verifies the tools/list_changed notification shape:
// jsonrpc:2.0, no id field, correct method name.
// "Servers that declared listChanged SHOULD send a notification when tool set changes."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L139
func TestToolsListChanged_notificationShape(t *testing.T) {
	srv := newTestServer(t, server.Params{})
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
		if v, _ := found["jsonrpc"].(string); v != transport.JsonRPCVersion {
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

// hasNotification returns true if any message has the given method and no id.
func hasNotification(msgs []map[string]any, method string) bool {
	for _, m := range msgs {
		if m["method"] == method && m["id"] == nil {
			return true
		}
	}
	return false
}
