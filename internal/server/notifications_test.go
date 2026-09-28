//go:build test

package server_test

import (
	"context"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
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

	if !hasNotification(msgs, "notifications/tools/list_changed") {
		t.Errorf("expected notifications/tools/list_changed after add_server; got: %v", msgs)
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
