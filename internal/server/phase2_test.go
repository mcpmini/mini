package server_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestExecProtected(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	perm := &config.PermissionsConfig{Protected: []string{"sendMessage"}}
	fake := &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: "sendMessage"}},
		Responses: map[string]json.RawMessage{"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"sent"}]}`)},
	}
	srv.AddConnection(ctx, config.ServerConfig{Name: "slack", Permissions: perm}, fake)

	resp := serve(t, srv, callTool("perm_call", map[string]any{
		"server": "slack",
		"tool":   "sendMessage",
		"params": map[string]any{"text": "hello"},
	}))

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
}

func TestExecProtectedRejectsOpenTool(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	// default permission is open
	srv.AddConnection(ctx, config.ServerConfig{Name: "ci"}, fakeConn("getBuild"))

	resp := serve(t, srv, callTool("perm_call", map[string]any{
		"server": "ci",
		"tool":   "getBuild",
		"params": map[string]any{},
	}))

	text := toolResultText(t, resp)
	if text == "" {
		t.Fatal("expected error text")
	}
}

func TestConfigureSetProjection(t *testing.T) {
	srv := newTestServer(t)

	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection",
		"server": "ci",
		"tool":   "getBuild",
		"projection": map[string]any{
			"include": []string{"status", "branch"},
		},
	}))

	text := toolResultText(t, resp)
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if result["ok"] != true {
		t.Errorf("expected ok:true, got %v", result)
	}
}

func TestConfigureRemoveServer(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	srv.AddConnection(ctx, config.ServerConfig{Name: "ci"}, fakeConn("getBuild"))

	serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": "ci",
	}))

	resp2 := serve(t, srv, callTool("list", map[string]any{}))
	text := toolResultText(t, resp2)
	var results []any
	json.Unmarshal([]byte(text), &results)
	if len(results) != 0 {
		t.Errorf("expected 0 tools after remove, got %d", len(results))
	}
}

func TestDiscoverDetail(t *testing.T) {
	srv := newTestServer(t)
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "getBuild", Description: "Get a build by ID",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)},
		},
		Responses: make(map[string]json.RawMessage),
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "ci"}, conn)
	resp := serve(t, srv, callTool("list", map[string]any{"tool": "ci.getBuild", "detail": true}))
	text := toolResultText(t, resp)
	var detail map[string]any
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if detail["name"] != "ci.getBuild" {
		t.Errorf("unexpected name: %v", detail["name"])
	}
	if detail["inputSchema"] == nil {
		t.Error("expected inputSchema in detail response")
	}
}
