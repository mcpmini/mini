//go:build test

package server_test

import (
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

// proxyHarness drives one mini server whose upstreams are fake connections, over stdio
// (call, setProjection) or as HTTP sessions (session).
type proxyHarness struct {
	t   *testing.T
	srv *server.Server
}

func newProxyHarness(t *testing.T, srv *server.Server) *proxyHarness {
	return &proxyHarness{t: t, srv: srv}
}

// addUpstream connects a fake upstream named serverName whose tools answer every call
// with result, a raw CallToolResult.
func (h *proxyHarness) addUpstream(serverName, result string, tools ...string) *transport.FakeConnection {
	h.t.Helper()
	conn := fakeConn(tools...)
	conn.Responses["tools/call"] = json.RawMessage(result)
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

// call returns the CallToolResult of one tools/call over stdio.
func (h *proxyHarness) call(tool string, args map[string]any) map[string]any {
	h.t.Helper()
	return proxyResultOf(h.t, serve(h.t, h.srv, callTool(tool, argsOrEmpty(args))))
}

func (h *proxyHarness) callText(tool string, args map[string]any) string {
	h.t.Helper()
	return toolResultText(h.t, serve(h.t, h.srv, callTool(tool, argsOrEmpty(args))))
}

// session initializes an HTTP session; its projections apply to that session only.
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

// textResult is a CallToolResult holding one text block.
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
