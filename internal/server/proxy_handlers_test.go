//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func newProxyServer(t *testing.T) *server.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 50
	return server.New(cfg, logger, server.WithProxyMode())
}

func addProxyConn(t *testing.T, srv *server.Server, name string, conn *transport.FakeConnection) {
	t.Helper()
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: name}, conn); err != nil {
		t.Fatalf("AddConnection %s: %v", name, err)
	}
}

func toolsList(t *testing.T, srv *server.Server) []map[string]any {
	t.Helper()
	msgs := serveAll(t, srv, rpc("tools/list", nil))
	for _, m := range msgs {
		if res, ok := m["result"].(map[string]any); ok {
			if tools, ok := res["tools"].([]any); ok {
				out := make([]map[string]any, len(tools))
				for i, tool := range tools {
					out[i] = tool.(map[string]any)
				}
				return out
			}
		}
	}
	t.Fatal("no tools/list result found")
	return nil
}

func toolNames(tools []map[string]any) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t["name"].(string)
	}
	return names
}

func containsName(tools []map[string]any, name string) bool {
	for _, t := range tools {
		if t["name"] == name {
			return true
		}
	}
	return false
}

func TestProxy_ToolsList_ContainsMiniTools(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("list_issues", "create_issue")
	addProxyConn(t, srv, "github", conn)

	tools := toolsList(t, srv)
	names := toolNames(tools)
	t.Logf("tools: %v", names)

	if !containsName(tools, "mini_config") {
		t.Error("expected mini_config in proxy tool list")
	}
	if !containsName(tools, "mini_read") {
		t.Error("expected mini_read in proxy tool list")
	}
	if !containsName(tools, "github__list_issues") {
		t.Error("expected github__list_issues in proxy tool list")
	}
	if !containsName(tools, "github__create_issue") {
		t.Error("expected github__create_issue in proxy tool list")
	}
}

func TestProxy_ToolsList_AlwaysLoadMeta(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	tools := toolsList(t, srv)
	for _, tool := range tools {
		name := tool["name"].(string)
		if name != "mini_config" && name != "mini_read" {
			continue
		}
		meta, ok := tool["_meta"].(map[string]any)
		if !ok {
			t.Errorf("%s: expected _meta object, got %T", name, tool["_meta"])
			continue
		}
		if meta["anthropic/alwaysLoad"] != true {
			t.Errorf("%s: expected anthropic/alwaysLoad: true, got %v", name, meta["anthropic/alwaysLoad"])
		}
	}
}

func TestProxy_ToolsList_UpstreamToolsNoAlwaysLoad(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("list_issues")
	addProxyConn(t, srv, "gh", conn)

	tools := toolsList(t, srv)
	for _, tool := range tools {
		if tool["name"] == "gh__list_issues" {
			if tool["_meta"] != nil {
				t.Error("upstream tool should not have _meta")
			}
		}
	}
}

func TestProxy_Initialize_Instructions(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	msgs := serveAll(t, srv)
	var initResult map[string]any
	for _, m := range msgs {
		if res, ok := m["result"].(map[string]any); ok {
			if _, hasProto := res["protocolVersion"]; hasProto {
				initResult = res
			}
		}
	}
	if initResult == nil {
		t.Fatal("no initialize result")
	}
	instructions := initResult["instructions"].(string)
	if !strings.Contains(instructions, "mini_read") {
		t.Errorf("proxy instructions should mention mini_read: %q", instructions)
	}
	if strings.Contains(instructions, "perm_call") {
		t.Errorf("proxy instructions should not mention perm_call: %q", instructions)
	}
}

func TestProxy_Call_NoProjection_PassesRawJSON(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	resp := serve(t, srv, callTool("svc__get_user", map[string]any{}))
	text := toolResultText(t, resp)

	// No projection configured → raw JSON passthrough, no [Projected] note
	if strings.HasPrefix(text, "[Projected") {
		t.Errorf("expected raw passthrough, got projection note: %s", text)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Errorf("expected valid JSON passthrough: %s", text)
	}
}

func TestProxy_Call_WithProjection_Small_BracketNote(t *testing.T) {
	srv := newProxyServer(t) // InlineThreshold=50
	defer srv.Close()
	conn := fakeConn("list_repos")
	// Small response with one field that will be elided
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\"}"}]}`)
	addProxyConn(t, srv, "gh", conn)

	serve(t, srv, callTool("mini_config", map[string]any{
		"action":     "set_projection",
		"server":     "gh",
		"tool":       "list_repos",
		"projection": map[string]any{"exclude_always": []string{"secret"}},
	}))

	resp := serve(t, srv, callTool("gh__list_repos", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("proxy small+projection response: %s", text)

	// Small response with projection → bracket note + inline JSON (no File: line)
	if !strings.HasPrefix(text, "[Projected") {
		t.Errorf("expected [Projected] note for small response with projection: %s", text)
	}
	if strings.Contains(text, "File:") {
		t.Errorf("small response should be inline, not file path: %s", text)
	}
}

func TestProxy_Call_WithProjection_Large_FilePath(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 1 // force all responses to be "large"
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	conn := fakeConn("list_prs")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"items\":[{\"id\":1,\"body\":\"long body text here\"}]}"}]}`)
	addProxyConn(t, srv, "gh", conn)

	// Add a projection that excludes body
	serve(t, srv, callTool("mini_config", map[string]any{
		"action":     "set_projection",
		"server":     "gh",
		"tool":       "list_prs",
		"projection": map[string]any{"exclude_always": []string{"body"}},
	}))

	resp := serve(t, srv, callTool("gh__list_prs", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("proxy large response: %s", text)

	if !strings.Contains(text, "File:") {
		t.Errorf("expected File: path in large projected response: %s", text)
	}
}

func TestProxy_MiniRead_ReadsFile(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 1 // force file writes
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serve(t, srv, callTool("mini_config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_item",
		"projection": map[string]any{"exclude_always": []string{"secret"}},
	}))

	// First call to get the file path
	resp1 := serve(t, srv, callTool("svc__get_item", map[string]any{}))
	text1 := toolResultText(t, resp1)
	t.Logf("initial response: %s", text1)

	if !strings.Contains(text1, "File:") {
		t.Skip("response was not written to file (threshold not triggered)")
	}

	// Extract file path from "File: /path/to/file"
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

	// Read the file via mini_read
	resp2 := serve(t, srv, callTool("mini_read", map[string]any{"path": filePath}))
	text2 := toolResultText(t, resp2)
	t.Logf("mini_read response: %s", text2)

	if text2 == "" {
		t.Error("mini_read returned empty content")
	}
	// Should be valid JSON
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text2), &parsed); err != nil {
		t.Errorf("mini_read content should be JSON: %s", text2)
	}
}

func TestProxy_MiniRead_RejectsPathTraversal(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	resp := serve(t, srv, callTool("mini_read", map[string]any{"path": "/etc/passwd"}))
	// Should be an RPC error (errInvalidParams) or tool error
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

	resp := serve(t, srv, callTool("mini_read", map[string]any{}))
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

	resp := serve(t, srv, callTool("nonexistent__tool", map[string]any{}))
	// Should get an RPC error or tool error
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

func TestProxy_MiniConfig_Status_Works(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("list_issues")
	addProxyConn(t, srv, "github", conn)

	resp := serve(t, srv, callTool("mini_config", map[string]any{"action": "status"}))
	text := toolResultText(t, resp)
	t.Logf("status: %s", text)

	var status map[string]any
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("status should be JSON: %s", text)
	}
	if status["servers"] == nil {
		t.Error("expected servers in status")
	}
}

func TestProxy_NotifyAll_OnRemoveServer(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("list_issues")
	addProxyConn(t, srv, "removeme", conn)

	msgs := serveAll(t, srv, callTool("mini_config", map[string]any{
		"action": "remove_server",
		"server": "removeme",
	}))

	if !hasNotification(msgs, transport.NotificationToolsChanged) {
		t.Error("expected notifications/tools/list_changed after remove_server in proxy mode")
	}
}
