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

func TestList_hidden_includesHiddenTools(t *testing.T) {
	srv := newTestServer(t)
	perm := &config.PermissionsConfig{Hidden: []string{"secretTool"}}
	srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc", Permissions: perm}, fakeConn("openTool", "secretTool"))

	text := toolResultText(t, serve(t, srv, callTool("list", map[string]any{"hidden": true})))
	var results []map[string]any
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("expected JSON array: %s", text)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 tools with hidden:true, got %d: %s", len(results), text)
	}
}

func TestList_hidden_disabledByConfig_returnsError(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DisableListHidden = true
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	resp := serve(t, srv, callTool("list", map[string]any{"hidden": true}))
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true when disable_list_hidden=true, got: %v", result)
	}
}

func TestList_detail_returnsSchema(t *testing.T) {
	srv := newTestServer(t)
	srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, fakeConn("myTool"))

	text := toolResultText(t, serve(t, srv, callTool("list", map[string]any{"tool": "svc.myTool", "detail": true})))
	var detail map[string]any
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("expected JSON object for detail: %s", text)
	}
	if detail["name"] != "svc.myTool" {
		t.Errorf("expected name=svc.myTool, got: %v", detail["name"])
	}
	if detail["inputSchema"] == nil {
		t.Error("expected inputSchema in detail response")
	}
}

func TestBuildEnvelope_linesFormat(t *testing.T) {
	srv := newTestServer(t)
	fake := fakeConn("list")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"[{\"id\":1,\"name\":\"foo\"},{\"id\":2,\"name\":\"bar\"}]"}]}`)
	srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, fake)

	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "list",
		"session_only": true, "projection": map[string]any{"format": "mini"},
	}))

	text := toolResultText(t, serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "list", "params": map[string]any{},
	})))
	if len(text) == 0 {
		t.Error("expected non-empty lines output")
	}
}
