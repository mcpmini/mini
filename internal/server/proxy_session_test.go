//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

type postMCPParams struct {
	T         *testing.T
	Server    *server.Server
	SessionID string
	Msg       any
}

func postMCP(params postMCPParams) map[string]any {
	params.T.Helper()
	b, _ := json.Marshal(params.Msg)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", params.SessionID)
	w := httptest.NewRecorder()
	params.Server.ServeHTTP(w, req)
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
	return resp
}

func initMsg(compact bool) map[string]any {
	params := map[string]any{
		"protocolVersion": transport.LegacyVersions[0],
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}
	if compact {
		params[transport.ToolModeParam] = transport.ToolModeCompactValue
	}
	return map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": 0, "method": "initialize", "params": params}
}

func toolsListMsg() map[string]any {
	return map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": 1, "method": "tools/list"}
}

func TestHTTPSession_InheritsServerCompactMode(t *testing.T) {
	srv := newTestServer(t, server.Params{ToolMode: transport.ToolModeCompact})
	defer srv.Close()
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "gh", Connection: fakeConn("list_issues")})

	const sessionID = "11111111-1111-1111-1111-111111111111"
	postMCP(
		postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: initMsg(false)},
	) // no wire signal: must inherit server default

	tools := extractToolNames(postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: toolsListMsg()}))
	if !hasToolName(tools, "call") {
		t.Errorf("HTTP session should inherit server-level compact mode, got %v", tools)
	}
	for _, n := range tools {
		if strings.Contains(n, "__") {
			t.Errorf("compact session should not expose upstream tools, got %v", tools)
		}
	}
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

func TestHTTPSession_DoubleInitialize_ModeLockedToFirst(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "gh", Connection: fakeConn("list_issues")})

	const sessionID = "22222222-2222-2222-2222-222222222222"
	postMCP(
		postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: initMsg(false)},
	) // first init: proxy mode (no signal → server default)

	// Second initialize sends compact signal — must not flip the session mode.
	secondInit := initMsg(true) // compact signal
	secondInit["id"] = 99
	resp := postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: secondInit})
	if resp["error"] != nil {
		t.Errorf("second initialize should not return error: %v", resp["error"])
	}

	tools := extractToolNames(postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: toolsListMsg()}))
	for _, n := range tools {
		if n == "call" || n == "list" || n == "perm_call" {
			t.Errorf("session should remain in proxy mode after double-initialize, got compact tool %q", n)
		}
	}
	found := false
	for _, n := range tools {
		if strings.Contains(n, "__") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("session should remain in proxy mode after double-initialize, got tools: %v", tools)
	}
}

func TestProxy_SessionProjection_FieldExclusionPersistsAcrossCalls(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"topsecret\",\"name\":\"foo\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	const sessionID = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: initMsg(false)})

	postMCP(postMCPParams{
		T: t, Server: srv, SessionID: sessionID, Msg: map[string]any{
			"jsonrpc": transport.JsonRPCVersion, "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "config", "arguments": map[string]any{
				"action": "set_projection", "server": "svc", "tool": "get_item",
				"projection": map[string]any{"exclude": []string{"secret"}}, "session_only": true,
			}},
		},
	})

	for i := range 3 {
		resp := postMCP(postMCPParams{
			T: t, Server: srv, SessionID: sessionID, Msg: map[string]any{
				"jsonrpc": transport.JsonRPCVersion, "id": 2 + i, "method": "tools/call",
				"params": map[string]any{"name": "svc__get_item", "arguments": map[string]any{}},
			},
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
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"topsecret\",\"name\":\"foo\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	const sessionA = "aaaaaaaa-aaaa-aaaa-aaaa-000000000001"
	const sessionB = "bbbbbbbb-bbbb-bbbb-bbbb-000000000002"
	postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionA, Msg: initMsg(false)})
	postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionB, Msg: initMsg(false)})

	postMCP(postMCPParams{
		T: t, Server: srv, SessionID: sessionA, Msg: map[string]any{
			"jsonrpc": transport.JsonRPCVersion, "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "config", "arguments": map[string]any{
				"action": "set_projection", "server": "svc", "tool": "get_item",
				"projection": map[string]any{"exclude": []string{"secret"}}, "session_only": true,
			}},
		},
	})

	callItem := func(sid string, id int) string {
		resp := postMCP(postMCPParams{
			T: t, Server: srv, SessionID: sid, Msg: map[string]any{
				"jsonrpc": transport.JsonRPCVersion, "id": id, "method": "tools/call",
				"params": map[string]any{"name": "svc__get_item", "arguments": map[string]any{}},
			},
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
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"topsecret\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	const sessionID = "cccccccc-cccc-cccc-cccc-000000000003"
	postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: initMsg(false)})

	postMCP(postMCPParams{
		T: t, Server: srv, SessionID: sessionID, Msg: map[string]any{
			"jsonrpc": transport.JsonRPCVersion, "id": 1, "method": "tools/call",
			"params": map[string]any{"name": "config", "arguments": map[string]any{
				"action": "set_projection", "server": "svc", "tool": "get_item",
				"projection": map[string]any{"exclude": []string{"secret"}}, "session_only": true,
			}},
		},
	})

	postMCP(postMCPParams{
		T: t, Server: srv, SessionID: sessionID, Msg: map[string]any{
			"jsonrpc": transport.JsonRPCVersion, "id": 2, "method": "tools/call",
			"params": map[string]any{"name": "config", "arguments": map[string]any{"action": "reload"}},
		},
	})

	resp := postMCP(postMCPParams{
		T: t, Server: srv, SessionID: sessionID, Msg: map[string]any{
			"jsonrpc": transport.JsonRPCVersion, "id": 3, "method": "tools/call",
			"params": map[string]any{"name": "svc__get_item", "arguments": map[string]any{}},
		},
	})
	res, _ := resp["result"].(map[string]any)
	content, _ := res["content"].([]any)
	text, _ := content[0].(map[string]any)["text"].(string)
	if strings.Contains(text, "topsecret") {
		t.Errorf("session projection wiped by reload: %s", text)
	}
}

func TestProxy_PerSession_ProxyAndStandardCoexist(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("list_issues")
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: "gh"}, conn); err != nil {
		t.Fatal(err)
	}

	const proxyID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const compactID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	postMCP(postMCPParams{T: t, Server: srv, SessionID: proxyID, Msg: initMsg(false)})
	postMCP(postMCPParams{T: t, Server: srv, SessionID: compactID, Msg: initMsg(true)})

	proxyTools := extractToolNames(postMCP(postMCPParams{T: t, Server: srv, SessionID: proxyID, Msg: toolsListMsg()}))
	compactTools := extractToolNames(
		postMCP(postMCPParams{T: t, Server: srv, SessionID: compactID, Msg: toolsListMsg()}),
	)

	if !hasToolName(proxyTools, "gh__list_issues") {
		t.Errorf("proxy session: expected gh__list_issues, got %v", proxyTools)
	}
	for _, n := range proxyTools {
		if n == "call" || n == "perm_call" {
			t.Errorf("proxy session should not expose compact tools, got %v", proxyTools)
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

// TestResolveToolMode_proxySignalOnCompactServer verifies that a client sending
// the "proxy" wire signal on a compact-configured server gets proxy mode, not
// compact mode. The "proxy" constant must have an effect on the wire.
func TestResolveToolMode_proxySignalOnCompactServer(t *testing.T) {
	srv := newTestServer(t, server.Params{ToolMode: transport.ToolModeCompact})
	defer srv.Close()
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "gh", Connection: fakeConn("list_issues")})

	params := map[string]any{
		"protocolVersion":       transport.LegacyVersions[0],
		"capabilities":          map[string]any{},
		"clientInfo":            map[string]any{"name": "test", "version": "0"},
		transport.ToolModeParam: transport.ToolModeProxyValue,
	}
	const sessionID = "22222222-2222-2222-2222-222222222222"
	postMCP(
		postMCPParams{
			T:         t,
			Server:    srv,
			SessionID: sessionID,
			Msg: map[string]any{
				"jsonrpc": transport.JsonRPCVersion,
				"id":      0,
				"method":  "initialize",
				"params":  params,
			},
		},
	)

	tools := extractToolNames(postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: toolsListMsg()}))
	if !hasToolName(tools, "gh__list_issues") {
		t.Errorf("proxy wire signal on compact server should yield proxy session, got tools: %v", tools)
	}
	for _, n := range tools {
		if n == "call" || n == "perm_call" {
			t.Errorf("session with proxy wire signal should not expose compact tools, got: %v", tools)
			break
		}
	}
}

func TestResolveToolMode_unrecognizedSignal_fallsBackToServerDefault(t *testing.T) {
	srv := newTestServer(t, server.Params{ToolMode: transport.ToolModeCompact})
	defer srv.Close()
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "gh", Connection: fakeConn("list_issues")})

	params := map[string]any{
		"protocolVersion":       transport.LegacyVersions[0],
		"capabilities":          map[string]any{},
		"clientInfo":            map[string]any{"name": "test", "version": "0"},
		transport.ToolModeParam: "turbo",
	}
	const sessionID = "33333333-3333-3333-3333-333333333333"
	postMCP(
		postMCPParams{
			T:         t,
			Server:    srv,
			SessionID: sessionID,
			Msg: map[string]any{
				"jsonrpc": transport.JsonRPCVersion,
				"id":      0,
				"method":  "initialize",
				"params":  params,
			},
		},
	)

	tools := extractToolNames(postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: toolsListMsg()}))
	if !hasToolName(tools, "call") {
		t.Errorf("unrecognized signal should fall back to server default (compact), got tools: %v", tools)
	}
}

func TestProxy_Initialize_PerSessionInstructions(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	instructions := func(compact bool, sessionID string) string {
		resp := postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: initMsg(compact)})
		res, _ := resp["result"].(map[string]any)
		s, _ := res["instructions"].(string)
		return s
	}

	proxy := instructions(false, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	if !strings.Contains(proxy, "read") || strings.Contains(proxy, "perm_call") {
		t.Errorf("proxy instructions wrong: %q", proxy)
	}

	compact := instructions(true, "dddddddd-dddd-dddd-dddd-dddddddddddd")
	if !strings.Contains(compact, "perm_call") {
		t.Errorf("compact instructions wrong: %q", compact)
	}
}
