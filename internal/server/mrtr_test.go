//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

const (
	inputRequiredResult = `{"resultType":"input_required","inputRequests":{"r1":{"method":"elicitation/create",` +
		`"params":{"message":"Proceed?","requestedSchema":{"type":"object"}}}},"requestState":"opaque-123"}`
	inputRequiredMessage = "svc asked for user input, which mini can only pass to an agent using MCP 2026-07-28 in proxy mode"
	legacyToolCallParams = `{"name":"confirm","arguments":{}}`
)

type countingConn struct {
	*transport.FakeConnection
	toolCalls atomic.Int32
}

func (c *countingConn) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if method == "tools/call" {
		c.toolCalls.Add(1)
	}
	return c.FakeConnection.Call(ctx, method, params)
}

func addInputRequiredUpstream(t *testing.T, srv *server.Server) *countingConn {
	t.Helper()
	conn := &countingConn{FakeConnection: fakeConn("confirm")}
	conn.Responses["tools/call"] = json.RawMessage(inputRequiredResult)
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

func mrtrCallFields(name string, arguments map[string]any) map[string]any {
	return map[string]any{
		"name":           name,
		"arguments":      arguments,
		"inputResponses": map[string]any{"r1": map[string]any{"action": "accept"}},
		"requestState":   "opaque-123",
	}
}

func modernMRTRCall(id int, name string, arguments map[string]any) []byte {
	fields := mrtrCallFields(name, arguments)
	fields["_meta"] = map[string]any{
		"io.modelcontextprotocol/protocolVersion": transport.ModernVersions[0],
		"io.modelcontextprotocol/clientCapabilities": map[string]any{
			"elicitation": map[string]any{"form": map[string]any{}},
			"roots":       map[string]any{},
			"sampling":    map[string]any{},
		},
	}
	return modernRPC("tools/call", id, fields)
}

func TestModernInputRequiredPassesThroughUnchanged(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	conn := addInputRequiredUpstream(t, srv)
	out := serveModernRaw(t, srv, modernMRTRCall(1, "svc__confirm", map[string]any{}))
	if got := rawResultByID(t, out, "1"); string(got) != inputRequiredResult {
		t.Fatalf("modern agent result = %s, want upstream bytes %s", got, inputRequiredResult)
	}
	if calls := conn.toolCalls.Load(); calls != 1 {
		t.Fatalf("upstream tools/call count = %d, want 1", calls)
	}
	if errs := upstreamErrorCount(t, srv, "svc"); errs != 0 {
		t.Errorf("input request counted as %v upstream errors, want 0", errs)
	}
	var forwarded struct {
		Meta           map[string]json.RawMessage `json:"_meta"`
		InputResponses json.RawMessage            `json:"inputResponses"`
		RequestState   json.RawMessage            `json:"requestState"`
	}
	if err := json.Unmarshal(conn.LastParams, &forwarded); err != nil {
		t.Fatal(err)
	}
	if got := string(
		forwarded.Meta["io.modelcontextprotocol/clientCapabilities"],
	); got != `{"elicitation":{"form":{}}}` {
		t.Errorf("forwarded clientCapabilities = %s, want only elicitation", got)
	}
	if string(forwarded.InputResponses) != `{"r1":{"action":"accept"}}` ||
		string(forwarded.RequestState) != `"opaque-123"` {
		t.Errorf("forwarded retry fields = %s / %s", forwarded.InputResponses, forwarded.RequestState)
	}
}

func TestModernInputRequiredThroughDaemonSession(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	addInputRequiredUpstream(t, srv)
	created := serveHTTPPost(srv, initRequest(), "")
	sessionID := created.Header().Get("Mcp-Session-Id")
	response := serveHTTPPost(srv, modernMRTRCall(2, "svc__confirm", map[string]any{}), sessionID)
	var message struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&message); err != nil {
		t.Fatal(err)
	}
	if string(message.Result) != inputRequiredResult {
		t.Fatalf("daemon result = %s, want upstream bytes", message.Result)
	}
}

func TestInputRequiredBecomesToolErrorForLegacyAgents(t *testing.T) {
	t.Run("proxy mode", func(t *testing.T) {
		srv := newTestServer(t, server.Params{})
		conn := addInputRequiredUpstream(t, srv)
		resp := serveProxy(t, srv, rpc("tools/call", mrtrCallFields("svc__confirm", map[string]any{})))
		assertInputRequiredToolError(t, resp)
		if string(conn.LastParams) != legacyToolCallParams {
			t.Errorf("legacy agent forwarded %s, want %s", conn.LastParams, legacyToolCallParams)
		}
	})
	t.Run("compact mode", func(t *testing.T) {
		srv := newTestServer(t, server.Params{})
		conn := addInputRequiredUpstream(t, srv)
		resp := serve(
			t,
			srv,
			callTool("call", map[string]any{"server": "svc", "tool": "confirm", "params": map[string]any{}}),
		)
		assertInputRequiredToolError(t, resp)
		if string(conn.LastParams) != legacyToolCallParams {
			t.Errorf("compact call forwarded %s, want %s", conn.LastParams, legacyToolCallParams)
		}
	})
}

func TestModernCompactInputRequiredBecomesToolError(t *testing.T) {
	srv := newTestServer(t, server.Params{ToolMode: transport.ToolModeCompact})
	addInputRequiredUpstream(t, srv)
	call := modernMRTRCall(1, "call", map[string]any{"server": "svc", "tool": "confirm"})
	resp := responsesByID(parseMessages(serveModernRaw(t, srv, call)))["1"]
	assertInputRequiredToolError(t, resp)
}

func assertInputRequiredToolError(t *testing.T, resp map[string]any) {
	t.Helper()
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("input_required result isError = %v, want true: %v", result["isError"], resp)
	}
	if text := toolResultText(t, resp); !strings.Contains(text, inputRequiredMessage) {
		t.Errorf("tool error text = %q, want %q", text, inputRequiredMessage)
	}
}

func rawResultByID(t *testing.T, out []byte, id string) json.RawMessage {
	t.Helper()
	for _, line := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(line, &message) == nil && string(message.ID) == id {
			return message.Result
		}
	}
	t.Fatalf("no response with id %s in %s", id, out)
	return nil
}

func upstreamErrorCount(t *testing.T, srv *server.Server, name string) float64 {
	t.Helper()
	var status struct {
		Servers map[string]struct {
			Errors float64 `json:"errors"`
		} `json:"servers"`
	}
	text := toolResultText(t, serve(t, srv, callTool("config", map[string]any{"action": "status"})))
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("status is not JSON: %v", err)
	}
	return status.Servers[name].Errors
}
