//go:build test

package server_test

import (
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

type proxyHarness struct {
	t   *testing.T
	srv *server.Server
}

func newProxyHarness(t *testing.T, srv *server.Server) *proxyHarness {
	return &proxyHarness{t: t, srv: srv}
}

func (h *proxyHarness) addUpstream(serverName, callToolResult string, tools ...string) *transport.FakeConnection {
	h.t.Helper()
	conn := fakeConn(tools...)
	conn.Responses["tools/call"] = json.RawMessage(callToolResult)
	addProxyConn(addProxyConnParams{T: h.t, Server: h.srv, Name: serverName, Connection: conn})
	return conn
}

func (h *proxyHarness) setProjection(serverName, tool string, projection map[string]any) {
	h.t.Helper()
	resp := serve(h.t, h.srv, callTool("config", map[string]any{
		"action": "set_projection", "server": serverName, "tool": tool, "projection": projection,
	}))
	requireConfigOK(h.t, resp)
}

func (h *proxyHarness) call(tool string, args map[string]any) map[string]any {
	h.t.Helper()
	return proxyResultOf(h.t, serve(h.t, h.srv, callTool(tool, argsOrEmpty(args))))
}

func (h *proxyHarness) callText(tool string, args map[string]any) string {
	h.t.Helper()
	return toolResultText(h.t, serve(h.t, h.srv, callTool(tool, argsOrEmpty(args))))
}

func (h *proxyHarness) session(id string) *httpSession {
	h.t.Helper()
	postMCP(postMCPParams{T: h.t, Server: h.srv, SessionID: id, Msg: initMsg()})
	return &httpSession{h: h, id: id}
}

type httpSession struct {
	h      *proxyHarness
	id     string
	nextID int
}

func (s *httpSession) setProjection(serverName, tool string, projection map[string]any) {
	s.h.t.Helper()
	resp := s.call("config", map[string]any{
		"action": "set_projection", "server": serverName, "tool": tool,
		"projection": projection, "session_only": true,
	})
	requireConfigOK(s.h.t, resp)
}

func (s *httpSession) callText(tool string, args map[string]any) string {
	s.h.t.Helper()
	return toolResultText(s.h.t, s.call(tool, argsOrEmpty(args)))
}

func (s *httpSession) call(tool string, args map[string]any) map[string]any {
	s.nextID++
	return postMCP(postMCPParams{T: s.h.t, Server: s.h.srv, SessionID: s.id, Msg: map[string]any{
		"jsonrpc": transport.JsonRPCVersion, "id": s.nextID, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	}})
}

func textResult(text string) string {
	b, _ := json.Marshal(map[string]any{"content": []map[string]any{{"type": "text", "text": text}}})
	return string(b)
}

func requireConfigOK(t *testing.T, resp map[string]any) {
	t.Helper()
	result, _ := resp["result"].(map[string]any)
	if resp["error"] != nil || result == nil || result["isError"] == true {
		t.Fatalf("config call failed: %v", resp)
	}
}

func argsOrEmpty(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	return args
}
