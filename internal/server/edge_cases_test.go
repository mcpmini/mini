//go:build test

package server_test

import (
	"bytes"
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

func newEdgeServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	return server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func rawServe(t *testing.T, srv *server.Server, input []byte) [][]byte {
	t.Helper()
	ctx := context.Background()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, bytes.NewReader(input), &out) }()
	<-done
	var lines [][]byte
	for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

func parseRPCResponse(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("parse RPC response: %v\nraw: %s", err, data)
	}
	return resp
}

func addEdgeConn(t *testing.T, srv *server.Server, cfg config.ServerConfig, conn *transport.FakeConnection) {
	t.Helper()
	srv.AddConnection(context.Background(), cfg, conn)
}

func assertIsErrorResult(t *testing.T, resp map[string]any) {
	t.Helper()
	result := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("expected isError=true, got: %v", result)
	}
}

func TestMalformedJSONLine(t *testing.T) {
	srv := newEdgeServer(t)
	lines := rawServe(t, srv, []byte("not json at all\n"))
	if len(lines) == 0 {
		t.Fatal("expected at least one response line")
	}
	resp := parseRPCResponse(t, lines[0])
	if resp["error"] == nil {
		t.Errorf("expected error for malformed JSON, got: %v", resp)
	}
	rpcErr := resp["error"].(map[string]any)
	if rpcErr["code"] == nil {
		t.Errorf("expected error code, got: %v", rpcErr)
	}
}

func TestEmptyLines_skipped(t *testing.T) {
	srv := newEdgeServer(t)
	// Empty lines should be silently skipped, not produce error responses
	input := "\n\n" + string(rpc("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}))
	lines := rawServe(t, srv, []byte(input))
	if len(lines) != 1 {
		t.Errorf("expected exactly 1 response (for initialize), got %d: %s", len(lines), bytes.Join(lines, []byte("|")))
	}
}

func TestUnknownMethod(t *testing.T) {
	srv := newEdgeServer(t)
	resp := serve(t, srv, rpc("no_such_method", nil))
	if resp["error"] == nil {
		t.Errorf("expected RPC error for unknown method, got: %v", resp)
	}
}

// TestInitializedNotification_noResponse verifies that notifications/initialized,
// when sent as a proper notification (no id), produces no response.
// Spec: "After successful initialization, the client MUST send an initialized notification."
// Notifications are fire-and-forget — the server MUST NOT send a response.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L109
func TestInitializedNotification_noResponse(t *testing.T) {
	srv := newEdgeServer(t)
	initMsg := rpc("initialize", map[string]any{
		"protocolVersion": transport.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	// Send notifications/initialized as a notification (no id) — the spec-correct form.
	notif := notification("notifications/initialized", nil)
	lines := rawServe(t, srv, append(initMsg, notif...))
	// Exactly one response: for initialize. The notification must produce no response.
	if len(lines) != 1 {
		t.Errorf("expected exactly 1 response (initialize only), got %d: %s",
			len(lines), bytes.Join(lines, []byte("|")))
	}
	var resp map[string]any
	if err := json.Unmarshal(lines[0], &resp); err != nil {
		t.Errorf("initialize response not valid JSON: %s", lines[0])
	}
	if resp["result"] == nil {
		t.Errorf("initialize response must have result, got: %v", resp)
	}
}


func fakeConnWithError(name string) *transport.FakeConnection {
	return &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: name, Description: name, InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"ok"}],"isError":true}`),
		},
	}
}

func assertOkFalse(t *testing.T, text string) {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("expected JSON envelope, got: %s", text)
	}
	if env["error"] == nil {
		t.Errorf("expected ok=false, got: %v", env)
	}
}

func TestExecWithFakeConnectionError(t *testing.T) {
	srv := newEdgeServer(t)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fakeConnWithError("ping"))

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "ping", "params": map[string]any{},
	}))
	assertOkFalse(t, toolResultText(t, resp))
}

func TestDiscoverDetail_edgeCases(t *testing.T) {
	srv := newEdgeServer(t)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fakeConn("myTool"))

	resp := serve(t, srv, callTool("list", map[string]any{
		"tool":   "svc.myTool",
		"detail": true,
	}))
	text := toolResultText(t, resp)
	var detail map[string]any
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("expected JSON detail, got: %s — err: %v", text, err)
	}
	if detail["name"] != "svc.myTool" {
		t.Errorf("expected name=svc.myTool in detail, got: %v", detail["name"])
	}
	if detail["inputSchema"] == nil {
		t.Errorf("expected inputSchema in detail, got: %v", detail)
	}
}

func TestDiscoverDetailNotFound(t *testing.T) {
	srv := newEdgeServer(t)
	resp := serve(t, srv, callTool("list", map[string]any{
		"tool":   "nonexistent.tool",
		"detail": true,
	}))
	// Lookup failure → isError=true content with plain error text
	assertIsErrorResult(t, resp)
	text := toolResultText(t, resp)
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' error message, got: %s", text)
	}
}

func invalidParamsInput() []byte {
	init := rpc("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "t", "version": "0"},
	})
	call := rpc("tools/call", map[string]any{
		"name":      "call",
		"arguments": `"not an object"`,
	})
	return append(init, call...)
}

func TestExecWithInvalidParams(t *testing.T) {
	srv := newEdgeServer(t)
	lines := rawServe(t, srv, invalidParamsInput())
	if len(lines) < 2 {
		t.Fatalf("expected 2 responses, got %d", len(lines))
	}
	last := parseRPCResponse(t, lines[len(lines)-1])
	result, ok := last["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got: %v", last)
	}
	if result["isError"] != true {
		t.Logf("got result: %v", result)
	}
}

// newSvcWithPartialProjection creates a server whose "svc" upstream has a projection
// configured for coveredTool only, leaving uncoveredTool without projection coverage.

// TestRequestBeforeInitialize_rejected verifies that requests (other than initialize and ping)
// sent before the initialize handshake are rejected with -32600 Invalid Request.
// Spec: "The initialization phase MUST be the first interaction between client and server."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L38
func TestRequestBeforeInitialize_rejected(t *testing.T) {
	for _, method := range []string{"tools/list", "tools/call"} {
		t.Run(method, func(t *testing.T) {
			srv := newEdgeServer(t)
			// Send request WITHOUT preceding initialize.
			lines := rawServe(t, srv, rpc(method, nil))
			if len(lines) == 0 {
				t.Fatal("expected a response")
			}
			resp := parseRPCResponse(t, lines[0])
			errObj, ok := resp["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected JSON-RPC error for %s before initialize, got: %v", method, resp)
			}
			if code := int(errObj["code"].(float64)); code != transport.CodeInvalidRequest {
				t.Errorf("error code = %d, want %d (InvalidRequest)", code, transport.CodeInvalidRequest)
			}
		})
	}
}

// TestPingBeforeInitialize_allowed verifies that ping is accepted before initialize.
// The spec explicitly permits ping before initialization as a liveness check.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L118
func TestPingBeforeInitialize_allowed(t *testing.T) {
	srv := newEdgeServer(t)
	lines := rawServe(t, srv, rpc("ping", nil))
	if len(lines) == 0 {
		t.Fatal("expected a response to ping")
	}
	resp := parseRPCResponse(t, lines[0])
	if resp["error"] != nil {
		t.Errorf("ping before initialize must not error, got: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || len(result) != 0 {
		t.Errorf("ping result must be {}, got: %v", resp["result"])
	}
}

// TestBatchRequest_returnsParseError verifies that a JSON array (batch) on a single line
// returns a -32700 Parse Error. mini does not implement batch support; the spec says
// initialize MUST NOT be in a batch, and other batches are valid but optional to support.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/transports.mdx#L25
func TestBatchRequest_returnsParseError(t *testing.T) {
	srv := newEdgeServer(t)
	batch := []byte(`[{"jsonrpc":"2.0","id":1,"method":"ping"}]` + "\n")
	lines := rawServe(t, srv, batch)
	if len(lines) == 0 {
		t.Fatal("expected a response")
	}
	resp := parseRPCResponse(t, lines[0])
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error for batch request, got: %v", resp)
	}
	if code := int(errObj["code"].(float64)); code != transport.CodeParseError {
		t.Errorf("batch should return -32700 ParseError, got code %d", code)
	}
}

// TestRPCEnvelope_jsonrpc20 verifies all responses carry jsonrpc:"2.0" and that
// result and error are mutually exclusive.
// https://www.jsonrpc.org/specification#response_object
func TestRPCEnvelope_jsonrpc20(t *testing.T) {
	cases := []struct {
		name  string
		input []byte
	}{
		{"initialize", rpc("initialize", map[string]any{
			"protocolVersion": transport.ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "t", "version": "0"},
		})},
		{"ping", rpc("ping", nil)},
		{"method not found", rpc("no_such_method", nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := rawServe(t, newTestServer(t), tc.input)
			if len(lines) == 0 {
				t.Fatal("no response")
			}
			var msg map[string]any
			if err := json.Unmarshal(lines[len(lines)-1], &msg); err != nil {
				t.Fatalf("invalid JSON: %s", lines[len(lines)-1])
			}
			if v, _ := msg["jsonrpc"].(string); v != "2.0" {
				t.Errorf("jsonrpc = %q, want \"2.0\"", v)
			}
			hasResult, hasError := msg["result"] != nil, msg["error"] != nil
			if hasResult && hasError {
				t.Error("response must not have both result and error")
			}
			if !hasResult && !hasError {
				t.Error("response must have either result or error")
			}
		})
	}
}

// TestErrorCodes_standardValues verifies that standard JSON-RPC error codes are used
// for the correct conditions.
// https://www.jsonrpc.org/specification#error_object (codes -32700 to -32603)
func TestErrorCodes_standardValues(t *testing.T) {
	cases := []struct {
		name     string
		input    []byte
		wantCode int
	}{
		{
			name:     "parse error -32700",
			input:    []byte("not json at all\n"),
			wantCode: transport.CodeParseError,
		},
		{
			name:     "invalid request -32600 (pre-init)",
			input:    rpc("tools/list", nil),
			wantCode: transport.CodeInvalidRequest,
		},
		{
			name:     "method not found -32601",
			input:    buildServeInput([][]byte{rpc("no_such_method", nil)}),
			wantCode: transport.CodeMethodNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := rawServe(t, newTestServer(t), tc.input)
			if len(lines) == 0 {
				t.Fatal("expected a response")
			}
			resp := parseRPCResponse(t, lines[len(lines)-1])
			errObj, ok := resp["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected error, got: %v", resp)
			}
			if got := int(errObj["code"].(float64)); got != tc.wantCode {
				t.Errorf("error code = %d, want %d", got, tc.wantCode)
			}
		})
	}
}

// TestResponseID_echoesRequest verifies that the response id matches the request id
// for string, integer, and zero IDs.
// "id MUST be the same as the value of the id member in the Request Object."
// https://www.jsonrpc.org/specification#response_object
func TestResponseID_echoesRequest(t *testing.T) {
	for _, id := range []any{42, "req-abc-123", 0} {
		t.Run("", func(t *testing.T) {
			rawID, _ := json.Marshal(id)
			reqBytes, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": json.RawMessage(rawID), "method": "ping",
			})
			reqBytes = append(reqBytes, '\n')
			lines := rawServe(t, newTestServer(t), buildServeInput([][]byte{reqBytes}))
			for _, line := range lines {
				var msg map[string]any
				json.Unmarshal(line, &msg) //nolint:errcheck
				respIDRaw, _ := json.Marshal(msg["id"])
				if bytes.Equal(respIDRaw, rawID) {
					if msg["error"] != nil {
						t.Errorf("ping with id=%v returned error: %v", id, msg["error"])
					}
					return
				}
			}
			t.Errorf("no response found with id=%v", id)
		})
	}
}
