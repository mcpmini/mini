//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/transport/transporttest"
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

// mrtrCallParams is an agent's retry of an input_required tool call. Capabilities stay raw so a
// test can show which of them reach the upstream.
type mrtrCallParams struct {
	Name           string          `json:"name"`
	Arguments      map[string]any  `json:"arguments"`
	Meta           *mrtrCallMeta   `json:"_meta,omitempty"`
	InputResponses json.RawMessage `json:"inputResponses"`
	RequestState   json.RawMessage `json:"requestState"`
}

type mrtrCallMeta struct {
	ClientCapabilities json.RawMessage `json:"io.modelcontextprotocol/clientCapabilities,omitempty"`
}

func mrtrRetry(name string, arguments map[string]any) mrtrCallParams {
	return mrtrCallParams{
		Name:           name,
		Arguments:      arguments,
		InputResponses: json.RawMessage(`{"r1":{"action":"accept"}}`),
		RequestState:   json.RawMessage(`"opaque-123"`),
	}
}

func modernMRTRCall(id int, name string, arguments map[string]any) []byte {
	params := mrtrRetry(name, arguments)
	params.Meta = &mrtrCallMeta{
		ClientCapabilities: json.RawMessage(`{"elicitation":{"form":{}},"roots":{},"sampling":{}}`),
	}
	return transporttest.ModernRequest(id, "tools/call", params)
}

func TestModernInputRequiredPassesThroughUnchanged(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	conn := addInputRequiredUpstream(t, srv)
	response := responsesByID(serveModern(t, srv, modernMRTRCall(1, "svc__confirm", map[string]any{})))[1]
	if got := response.Result; string(got) != inputRequiredResult {
		t.Fatalf("modern agent result = %s, want upstream bytes %s", got, inputRequiredResult)
	}
	if calls := conn.toolCalls.Load(); calls != 1 {
		t.Fatalf("upstream tools/call count = %d, want 1", calls)
	}
	if errs := upstreamErrorCount(t, srv, "svc"); errs != 0 {
		t.Errorf("input request counted as %v upstream errors, want 0", errs)
	}
	var forwarded mrtrCallParams
	if err := json.Unmarshal(conn.LastParams, &forwarded); err != nil {
		t.Fatal(err)
	}
	if forwarded.Meta == nil || string(forwarded.Meta.ClientCapabilities) != `{"elicitation":{"form":{}}}` {
		t.Errorf("forwarded params = %s, want only the elicitation capability", conn.LastParams)
	}
	if string(forwarded.InputResponses) != `{"r1":{"action":"accept"}}` ||
		string(forwarded.RequestState) != `"opaque-123"` {
		t.Errorf("forwarded retry fields = %s / %s", forwarded.InputResponses, forwarded.RequestState)
	}
}

func TestModernInputRequestRoundTripThroughModernHTTPUpstream(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DangerousAllowPrivateURLs = true
	srv := newTestServer(t, server.Params{Config: cfg})
	retries := make(chan mrtrCallParams, 1)
	srv.ConnectUpstreams(t.Context(), []config.ServerConfig{
		{Name: "svc", Transport: "http", URL: modernElicitingUpstream(t, retries)},
	})
	// Proxy-mode tools/list waits for a connecting upstream, so the calls below find it ready.
	serveModern(t, srv, transporttest.ModernRequest(0, "tools/list", nil))
	call := transport.ToolCallParams{Name: "svc__confirm", Arguments: map[string]any{}}
	first := responsesByID(serveModern(t, srv, transporttest.ModernRequest(1, "tools/call", call)))[1]
	if string(first.Result) != inputRequiredResult {
		t.Fatalf("first call result = %s, want the upstream input request %s", first.Result, inputRequiredResult)
	}
	second := responsesByID(serveModern(t, srv, modernMRTRCall(2, "svc__confirm", map[string]any{})))[2]
	if result := decodeResult[transport.ToolCallResult](t, second); result.IsError || len(result.Content) == 0 ||
		result.Content[0].Text != "confirmed" {
		t.Fatalf("retry result = %s, want the completed call", second.Result)
	}
	retry := <-retries
	if string(retry.RequestState) != `"opaque-123"` || string(retry.InputResponses) != `{"r1":{"action":"accept"}}` ||
		retry.Meta == nil || string(retry.Meta.ClientCapabilities) != `{"elicitation":{"form":{}}}` {
		t.Fatalf("upstream retry = %+v, want the agent's answer, request state and only elicitation", retry)
	}
}

// modernElicitingUpstream speaks 2026-07-28 and asks for input on a first call, completing the retry
// that carries the request state back.
func modernElicitingUpstream(t *testing.T, retries chan<- mrtrCallParams) string {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		writeUpstreamResult(t, w, req.ID, elicitingResult(t, req.Method, req.Params, retries))
	}))
	t.Cleanup(upstream.Close)
	return upstream.URL
}

func elicitingResult(t *testing.T, method string, params json.RawMessage, retries chan<- mrtrCallParams) any {
	switch method {
	case "server/discover":
		return transport.DiscoverResult{SupportedVersions: transport.ModernVersions}
	case "tools/list":
		return transport.ToolsListResult{Tools: []transport.ToolDefinition{
			{Name: "confirm", InputSchema: json.RawMessage(`{"type":"object"}`)},
		}}
	case "tools/call":
		var call mrtrCallParams
		if err := json.Unmarshal(params, &call); err != nil {
			t.Errorf("decode tools/call params: %v", err)
		}
		if len(call.RequestState) == 0 {
			return json.RawMessage(inputRequiredResult)
		}
		retries <- call
		return transport.ToolCallResult{Content: []transport.ContentItem{{Type: "text", Text: "confirmed"}}}
	}
	return struct{}{}
}

func writeUpstreamResult(t *testing.T, w http.ResponseWriter, id json.RawMessage, result any) {
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Errorf("encode upstream result: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).
		Encode(transport.Response{JsonRPC: transport.JsonRPCVersion, ID: id, Result: encoded}); err != nil {
		t.Errorf("write upstream result: %v", err)
	}
}

func TestModernInputRequiredThroughDaemonSession(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	addInputRequiredUpstream(t, srv)
	created := serveHTTPPost(srv, initRequest(), "")
	sessionID := created.Header().Get("Mcp-Session-Id")
	response := serveHTTPPost(srv, modernMRTRCall(2, "svc__confirm", map[string]any{}), sessionID)
	message := decodeRecorderMessage(t, response)
	if string(message.Result) != inputRequiredResult {
		t.Fatalf("daemon result = %s, want upstream bytes", message.Result)
	}
}

func TestInputRequiredBecomesToolErrorForLegacyAgents(t *testing.T) {
	t.Run("proxy mode", func(t *testing.T) {
		srv := newTestServer(t, server.Params{})
		conn := addInputRequiredUpstream(t, srv)
		resp := serveProxy(t, srv, rpc("tools/call", mrtrRetry("svc__confirm", map[string]any{})))
		assertInputRequiredToolError(t, legacyToolCallResult(t, resp))
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
		assertInputRequiredToolError(t, legacyToolCallResult(t, resp))
		if string(conn.LastParams) != legacyToolCallParams {
			t.Errorf("compact call forwarded %s, want %s", conn.LastParams, legacyToolCallParams)
		}
	})
}

func TestModernCompactInputRequiredBecomesToolError(t *testing.T) {
	srv := newTestServer(t, server.Params{ToolMode: transport.ToolModeCompact})
	addInputRequiredUpstream(t, srv)
	call := modernMRTRCall(1, "call", map[string]any{"server": "svc", "tool": "confirm"})
	response := responsesByID(serveModern(t, srv, call))[1]
	assertInputRequiredToolError(t, decodeResult[transport.ToolCallResult](t, response))
}

func assertInputRequiredToolError(t *testing.T, result transport.ToolCallResult) {
	t.Helper()
	if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, inputRequiredMessage) {
		t.Errorf("input_required result = %+v, want a tool error containing %q", result, inputRequiredMessage)
	}
}

func legacyToolCallResult(t *testing.T, resp map[string]any) transport.ToolCallResult {
	t.Helper()
	encoded, err := json.Marshal(resp["result"])
	if err != nil {
		t.Fatal(err)
	}
	var result transport.ToolCallResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatalf("decode tool result %s: %v", encoded, err)
	}
	return result
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
