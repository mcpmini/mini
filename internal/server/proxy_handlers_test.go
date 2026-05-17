//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

	if !containsName(tools, "config") {
		t.Error("expected config in proxy tool list")
	}
	if !containsName(tools, "read") {
		t.Error("expected read in proxy tool list")
	}
	if !containsName(tools, "github__list_issues") {
		t.Error("expected github__list_issues in proxy tool list")
	}
	if !containsName(tools, "github__create_issue") {
		t.Error("expected github__create_issue in proxy tool list")
	}
}

func TestProxy_ToolsList_MiniToolsNoMeta(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	tools := toolsList(t, srv)
	for _, tool := range tools {
		name := tool["name"].(string)
		if name == "config" || name == "read" {
			if tool["_meta"] != nil {
				t.Errorf("%s: expected no _meta, got %v", name, tool["_meta"])
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
	if !strings.Contains(instructions, "read") {
		t.Errorf("proxy instructions should mention read: %q", instructions)
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
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\"}"}]}`)
	addProxyConn(t, srv, "gh", conn)

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "gh",
		"tool":       "list_repos",
		"projection": map[string]any{"exclude_always": []string{"secret"}},
	}))

	resp := serve(t, srv, callTool("gh__list_repos", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("proxy small+projection response: %s", text)

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

	serve(t, srv, callTool("config", map[string]any{
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

func TestProxy_Call_Large_WithProjection_NoNote_FilePathOnly(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 1 // force all responses to file
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	conn := fakeConn("get_data")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"value\":\"data\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_data",
		"projection": map[string]any{"include": []string{"id", "value"}},
	}))

	resp := serve(t, srv, callTool("svc__get_data", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("large projection-no-note response: %s", text)

	if strings.HasPrefix(text, "[Projected") {
		t.Errorf("expected no [Projected] note when nothing elided: %s", text)
	}
	if !strings.HasPrefix(text, "File:") {
		t.Errorf("expected File: path for large response with projection: %s", text)
	}
}

func TestProxy_Call_WithTruncation_ProjectionNote(t *testing.T) {
	srv := newProxyServer(t) // InlineThreshold=50
	defer srv.Close()
	conn := fakeConn("get_issue")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"body\":\"this is a very long body that will be truncated\"}"}]}`)
	addProxyConn(t, srv, "gh", conn)

	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection",
		"server": "gh",
		"tool":   "get_issue",
		"projection": map[string]any{
			"string_limits": map[string]any{"body": 5},
		},
	}))

	resp := serve(t, srv, callTool("gh__get_issue", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("truncation note response: %s", text)

	if !strings.Contains(text, "truncated") {
		t.Errorf("expected 'truncated' in projection note: %s", text)
	}
	if !strings.Contains(text, "body") {
		t.Errorf("expected field name 'body' in projection note: %s", text)
	}
}

func TestProxy_Call_MiniFormat_RendersLines(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000 // keep inline
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_user",
		"projection": map[string]any{"format": "mini"},
	}))

	resp := serve(t, srv, callTool("svc__get_user", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("mini format response: %s", text)

	if !strings.Contains(text, "svc.get_user") {
		t.Errorf("mini format should contain server.tool header: %s", text)
	}
	if strings.HasPrefix(text, "{") {
		t.Errorf("mini format should not be raw JSON: %s", text)
	}
}

func TestProxy_Call_GlobalMiniFormat_Respected(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	cfg.ResponseFormat = "mini"
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithProxyMode())
	defer srv.Close()

	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_user",
		"projection": map[string]any{"exclude_always": []string{}},
	}))

	resp := serve(t, srv, callTool("svc__get_user", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("global mini format: %s", text)

	if strings.HasPrefix(text, "{") {
		t.Errorf("global mini format should not be raw JSON: %s", text)
	}
}

func TestProxy_Call_MiniFormat_PerSessionProxyMode(t *testing.T) {
	// Tests mini format via per-session proxy mode (daemon path: _mini_proxy_mode negotiated
	// in initialize, not via server-level WithProxyMode). This is the path used in production
	// when mini proxy connects to the daemon.
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 100000
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil))) // no WithProxyMode
	defer srv.Close()

	conn := fakeConn("list_items")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"[{\"id\":1,\"name\":\"foo\"},{\"id\":2,\"name\":\"bar\"}]"}]}`)
	addProxyConn(t, srv, "svc", conn)

	const sessionID = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
	postMCP(t, srv, sessionID, initMsg(true)) // negotiate proxy mode per-session

	postMCP(t, srv, sessionID, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "config",
			"arguments": map[string]any{
				"action": "set_projection", "server": "svc", "tool": "list_items",
				"projection": map[string]any{"format": "mini"}, "session_only": true,
			},
		},
	})

	resp := postMCP(t, srv, sessionID, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "svc__list_items", "arguments": map[string]any{}},
	})

	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("no content in response")
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	t.Logf("per-session mini format output: %q", text)

	if !strings.Contains(text, "svc.list_items") {
		t.Errorf("expected [svc.list_items] header in mini format output: %s", text)
	}
	if strings.HasPrefix(text, "{") {
		t.Errorf("expected mini text format, got JSON: %s", text)
	}
}

func TestProxy_SessionProjection_FieldExclusionPersistsAcrossCalls(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 100000
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"topsecret\",\"name\":\"foo\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	const sessionID = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	postMCP(t, srv, sessionID, initMsg(true))

	postMCP(t, srv, sessionID, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "config", "arguments": map[string]any{
			"action": "set_projection", "server": "svc", "tool": "get_item",
			"projection": map[string]any{"exclude_always": []string{"secret"}}, "session_only": true,
		}},
	})

	for i := range 3 {
		resp := postMCP(t, srv, sessionID, map[string]any{
			"jsonrpc": "2.0", "id": 2 + i, "method": "tools/call",
			"params": map[string]any{"name": "svc__get_item", "arguments": map[string]any{}},
		})
		res, _ := resp["result"].(map[string]any)
		content, _ := res["content"].([]any)
		text, _ := content[0].(map[string]any)["text"].(string)
		if strings.Contains(text, "topsecret") {
			t.Errorf("call %d: secret field leaked in response: %s", i+1, text)
		}
	}
}

func TestProxy_SessionProjection_IsolatedBetweenSessions(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 100000
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"topsecret\",\"name\":\"foo\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	const sessionA = "aaaaaaaa-aaaa-aaaa-aaaa-000000000001"
	const sessionB = "bbbbbbbb-bbbb-bbbb-bbbb-000000000002"
	postMCP(t, srv, sessionA, initMsg(true))
	postMCP(t, srv, sessionB, initMsg(true))

	// Only session A gets the projection
	postMCP(t, srv, sessionA, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "config", "arguments": map[string]any{
			"action": "set_projection", "server": "svc", "tool": "get_item",
			"projection": map[string]any{"exclude_always": []string{"secret"}}, "session_only": true,
		}},
	})

	callItem := func(sid string, id int) string {
		resp := postMCP(t, srv, sid, map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{"name": "svc__get_item", "arguments": map[string]any{}},
		})
		res, _ := resp["result"].(map[string]any)
		content, _ := res["content"].([]any)
		text, _ := content[0].(map[string]any)["text"].(string)
		return text
	}

	if strings.Contains(callItem(sessionA, 10), "topsecret") {
		t.Error("session A: projection should exclude secret")
	}
	if !strings.Contains(callItem(sessionB, 11), "topsecret") {
		t.Error("session B: should see full response (no projection set)")
	}
}

func TestProxy_Reload_PreservesSessionProjections(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 100000
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"topsecret\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	const sessionID = "cccccccc-cccc-cccc-cccc-000000000003"
	postMCP(t, srv, sessionID, initMsg(true))

	postMCP(t, srv, sessionID, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "config", "arguments": map[string]any{
			"action": "set_projection", "server": "svc", "tool": "get_item",
			"projection": map[string]any{"exclude_always": []string{"secret"}}, "session_only": true,
		}},
	})

	// Reload server-level projections — should not wipe session projections
	postMCP(t, srv, sessionID, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "config", "arguments": map[string]any{"action": "reload"}},
	})

	resp := postMCP(t, srv, sessionID, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "svc__get_item", "arguments": map[string]any{}},
	})
	res, _ := resp["result"].(map[string]any)
	content, _ := res["content"].([]any)
	text, _ := content[0].(map[string]any)["text"].(string)
	if strings.Contains(text, "topsecret") {
		t.Errorf("session projection wiped by reload: %s", text)
	}
}

func TestProxy_StandaloneServe_InheritsProxyMode(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	msgs := serveAll(t, srv, rpc("tools/list", nil))
	var tools []any
	for _, m := range msgs {
		if res, ok := m["result"].(map[string]any); ok {
			if t2, ok := res["tools"].([]any); ok {
				tools = t2
			}
		}
	}
	for _, tool := range tools {
		name := tool.(map[string]any)["name"].(string)
		if name == "perm_call" {
			t.Errorf("standalone proxy mode should not expose perm_call, got tools via serveAll")
		}
	}
}

func TestProxy_MiniConfig_Status_Works(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("list_issues")
	addProxyConn(t, srv, "github", conn)

	resp := serve(t, srv, callTool("config", map[string]any{"action": "status"}))
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

	msgs := serveAll(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": "removeme",
	}))

	if !hasNotification(msgs, transport.NotificationToolsChanged) {
		t.Error("expected notifications/tools/list_changed after remove_server in proxy mode")
	}
}

func postMCP(t *testing.T, srv *server.Server, sessionID string, msg any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(msg)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", sessionID)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
	return resp
}

func initMsg(proxyMode bool) map[string]any {
	params := map[string]any{
		"protocolVersion": transport.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}
	if proxyMode {
		params["_mini_proxy_mode"] = true
	}
	return map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": params}
}

func toolsListMsg() map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
}

func extractToolNames(resp map[string]any) []string {
	res, _ := resp["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.(map[string]any)["name"].(string)
	}
	return names
}

func hasToolName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func TestProxy_PerSession_ProxyAndStandardCoexist(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	conn := fakeConn("list_issues")
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh"}, conn); err != nil {
		t.Fatal(err)
	}

	const proxyID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const standardID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	postMCP(t, srv, proxyID, initMsg(true))
	postMCP(t, srv, standardID, initMsg(false))

	proxyTools := extractToolNames(postMCP(t, srv, proxyID, toolsListMsg()))
	standardTools := extractToolNames(postMCP(t, srv, standardID, toolsListMsg()))

	if !hasToolName(proxyTools, "gh__list_issues") {
		t.Errorf("proxy session: expected gh__list_issues, got %v", proxyTools)
	}
	for _, n := range proxyTools {
		if n == "call" || n == "perm_call" {
			t.Errorf("proxy session should not expose standard tools, got %v", proxyTools)
			break
		}
	}

	if !hasToolName(standardTools, "call") {
		t.Errorf("standard session: expected call tool, got %v", standardTools)
	}
	for _, n := range standardTools {
		if strings.Contains(n, "__") {
			t.Errorf("standard session should not expose upstream tools, got %v", standardTools)
			break
		}
	}
}

func TestProxy_Initialize_PerSessionInstructions(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	instructions := func(proxyMode bool, sessionID string) string {
		resp := postMCP(t, srv, sessionID, initMsg(proxyMode))
		res, _ := resp["result"].(map[string]any)
		s, _ := res["instructions"].(string)
		return s
	}

	proxy := instructions(true, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	if !strings.Contains(proxy, "read") || strings.Contains(proxy, "perm_call") {
		t.Errorf("proxy instructions wrong: %q", proxy)
	}

	std := instructions(false, "dddddddd-dddd-dddd-dddd-dddddddddddd")
	if !strings.Contains(std, "perm_call") {
		t.Errorf("standard instructions wrong: %q", std)
	}
}
