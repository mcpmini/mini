//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func TestConfigureSetProjection(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	writeTestServerConfig(t, srv, config.ServerConfig{Name: "ci"})

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection",
		"server": "ci",
		"tool":   "getBuild",
		"projection": map[string]any{
			"include_only": []string{"status", "branch"},
		},
	}))

	text := toolResultText(t, resp)
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if result["error"] != nil {
		t.Errorf("expected ok:true, got %v", result)
	}
}

func TestConfigureRemoveServer(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	ctx := context.Background()
	srv.AddConnection(ctx, config.ServerConfig{Name: "ci"}, fakeConn("getBuild"))

	serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": "ci",
	}))

	resp2 := serve(t, srv, rpc("tools/list", nil))
	text := toolListJSON(t, resp2)
	var results []any
	json.Unmarshal([]byte(text), &results)
	if len(results) != 2 {
		t.Errorf("expected only two builtins after remove, got %d", len(results))
	}
}
