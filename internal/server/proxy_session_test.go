//go:build test

package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func initMsg() map[string]any {
	params := map[string]any{
		"protocolVersion": transport.LegacyVersions[0],
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}
	return map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": 0, "method": "initialize", "params": params}
}

func toolsListMsg() map[string]any {
	return map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": 1, "method": "tools/list"}
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
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"topsecret\",\"name\":\"foo\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	const sessionID = "ffffffff-ffff-ffff-ffff-ffffffffffff"
	postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: initMsg()})

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
	postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionA, Msg: initMsg()})
	postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionB, Msg: initMsg()})

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
	postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: initMsg()})

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

func TestHTTPSessionIgnoresRemovedCompactSignal(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	fake := fakeConn("echo")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: fake})
	const sid = "11111111-1111-1111-1111-111111111111"
	init := initMsg()
	init["params"].(map[string]any)["_mini_tool_mode"] = "compact"
	resp := postMCP(postMCPParams{T: t, Server: srv, SessionID: sid, Msg: init})
	if resp["error"] != nil {
		t.Fatalf("initialize: %v", resp)
	}
	names := extractToolNames(postMCP(postMCPParams{T: t, Server: srv, SessionID: sid, Msg: toolsListMsg()}))
	if !hasToolName(names, "svc__echo") || !hasToolName(names, "config") || !hasToolName(names, "read") {
		t.Fatalf("native tools missing: %v", names)
	}
	for _, name := range []string{"list", "call", "perm_call"} {
		if hasToolName(names, name) {
			t.Fatalf("removed tool advertised: %s", name)
		}
		var request map[string]any
		if err := json.Unmarshal(
			callTool(name, map[string]any{"server": "svc", "tool": "echo"}),
			&request,
		); err != nil {
			t.Fatal(err)
		}
		got := postMCP(postMCPParams{T: t, Server: srv, SessionID: sid, Msg: request})
		requireRPCError(requireRPCErrorParams{T: t, Resp: got, WantCode: transport.CodeInvalidParams})
	}
	if len(fake.LastParams) != 0 {
		t.Fatal("removed interface reached upstream")
	}
}
