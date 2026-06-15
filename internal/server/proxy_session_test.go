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

func postMCP(t *testing.T, srv *server.Server, sessionID string, msg any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(msg)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", sessionID)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
	return resp
}

func initMsg(compact bool) map[string]any {
	params := map[string]any{
		"protocolVersion": transport.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}
	if compact {
		params[transport.ToolModeParam] = transport.ToolModeCompactValue
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
	postMCP(t, srv, sessionID, initMsg(false))

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
	postMCP(t, srv, sessionA, initMsg(false))
	postMCP(t, srv, sessionB, initMsg(false))

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
	postMCP(t, srv, sessionID, initMsg(false))

	postMCP(t, srv, sessionID, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "config", "arguments": map[string]any{
			"action": "set_projection", "server": "svc", "tool": "get_item",
			"projection": map[string]any{"exclude_always": []string{"secret"}}, "session_only": true,
		}},
	})

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

func TestProxy_PerSession_ProxyAndStandardCoexist(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()
	conn := fakeConn("list_issues")
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh"}, conn); err != nil {
		t.Fatal(err)
	}

	const passthroughID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const compactID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	postMCP(t, srv, passthroughID, initMsg(false))
	postMCP(t, srv, compactID, initMsg(true))

	passthroughTools := extractToolNames(postMCP(t, srv, passthroughID, toolsListMsg()))
	compactTools := extractToolNames(postMCP(t, srv, compactID, toolsListMsg()))

	if !hasToolName(passthroughTools, "gh__list_issues") {
		t.Errorf("passthrough session: expected gh__list_issues, got %v", passthroughTools)
	}
	for _, n := range passthroughTools {
		if n == "call" || n == "perm_call" {
			t.Errorf("passthrough session should not expose compact tools, got %v", passthroughTools)
			break
		}
	}

	if !hasToolName(compactTools, "call") {
		t.Errorf("compact session: expected call tool, got %v", compactTools)
	}
	for _, n := range compactTools {
		if strings.Contains(n, "__") {
			t.Errorf("compact session should not expose upstream tools, got %v", compactTools)
			break
		}
	}
}

func TestProxy_Initialize_PerSessionInstructions(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	instructions := func(compact bool, sessionID string) string {
		resp := postMCP(t, srv, sessionID, initMsg(compact))
		res, _ := resp["result"].(map[string]any)
		s, _ := res["instructions"].(string)
		return s
	}

	passthrough := instructions(false, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	if !strings.Contains(passthrough, "read") || strings.Contains(passthrough, "perm_call") {
		t.Errorf("passthrough instructions wrong: %q", passthrough)
	}

	compact := instructions(true, "dddddddd-dddd-dddd-dddd-dddddddddddd")
	if !strings.Contains(compact, "perm_call") {
		t.Errorf("compact instructions wrong: %q", compact)
	}
}
