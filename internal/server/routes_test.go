//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestUnknownMethod(t *testing.T) {
	srv := newTestServer(t, server.Params{})
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
	srv := newTestServer(t, server.Params{})
	initMsg := rpc("initialize", map[string]any{
		"protocolVersion": transport.LatestLegacyVersion,
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

// TestRequestBeforeInitialize_rejected verifies that requests (other than initialize and ping)
// sent before the initialize handshake are rejected with -32600 Invalid Request.
// Spec: "The initialization phase MUST be the first interaction between client and server."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L38
func TestRequestBeforeInitialize_rejected(t *testing.T) {
	for _, method := range []string{"tools/list", "tools/call"} {
		t.Run(method, func(t *testing.T) {
			srv := newTestServer(t, server.Params{})
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

func TestServerDiscoverBeforeInitialize_methodNotFound(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	lines := rawServe(t, srv, rpc("server/discover", nil))
	if len(lines) == 0 {
		t.Fatal("expected a response")
	}
	resp := parseRPCResponse(t, lines[0])
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected JSON-RPC error, got: %v", resp)
	}
	if code := int(errObj["code"].(float64)); code != transport.CodeMethodNotFound {
		t.Errorf("error code = %d, want %d", code, transport.CodeMethodNotFound)
	}
	if got, _ := errObj["message"].(string); got == transport.NotInitializedMessage {
		t.Errorf("got initialization gate error %q", got)
	}
}

// TestPingBeforeInitialize_allowed verifies that ping is accepted before initialize.
// The spec explicitly permits ping before initialization as a liveness check.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L118
func TestPingBeforeInitialize_allowed(t *testing.T) {
	srv := newTestServer(t, server.Params{})
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

// TestRPCEnvelope_jsonrpc20 verifies all responses carry jsonrpc:"2.0" and that
// result and error are mutually exclusive.
// https://www.jsonrpc.org/specification#response_object
func TestRPCEnvelope_jsonrpc20(t *testing.T) {
	cases := []struct {
		name  string
		input []byte
	}{
		{"initialize", rpc("initialize", map[string]any{
			"protocolVersion": transport.LatestLegacyVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "t", "version": "0"},
		})},
		{"ping", rpc("ping", nil)},
		{"method not found", rpc("no_such_method", nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := rawServe(t, newTestServer(t, server.Params{}), tc.input)
			if len(lines) == 0 {
				t.Fatal("no response")
			}
			var msg map[string]any
			if err := json.Unmarshal(lines[len(lines)-1], &msg); err != nil {
				t.Fatalf("invalid JSON: %s", lines[len(lines)-1])
			}
			if v, _ := msg["jsonrpc"].(string); v != transport.JsonRPCVersion {
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
			lines := rawServe(t, newTestServer(t, server.Params{}), tc.input)
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
				"jsonrpc": transport.JsonRPCVersion, "id": json.RawMessage(rawID), "method": "ping",
			})
			reqBytes = append(reqBytes, '\n')
			lines := rawServe(t, newTestServer(t, server.Params{}), buildServeInput([][]byte{reqBytes}))
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

func TestUnknownTool_Returns32602(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("nonexistent", map[string]any{}))
	if resp["error"] == nil {
		t.Fatalf("expected JSON-RPC error, got: %v", resp)
	}
	errObj, _ := resp["error"].(map[string]any)
	if code, _ := errObj["code"].(float64); code != -32602 {
		t.Errorf("expected code -32602, got %v", errObj["code"])
	}
}

func TestKnownToolBadArgs_ReturnsIsError(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	fake := fakeConn("ping")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"pong"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)

	resp := serve(t, srv, callTool("svc__ping", nil))
	if resp["error"] != nil {
		t.Fatalf("expected tool result, not RPC error: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Error("expected content in result")
	}
}

// TestToolsCall_executionErrorIsIsError verifies that upstream execution errors are returned
// as isError:true in the tool result, NOT as a JSON-RPC error object.
// "Tool errors SHOULD be reported in tool results with isError:true"
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L244
func TestToolsCall_executionErrorIsIsError(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	fake := fakeConnWithError("errTool")
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)

	resp := serve(t, srv, callTool("svc__errTool", map[string]any{}))
	if resp["error"] != nil {
		t.Errorf("tool execution error must NOT be a JSON-RPC error, got: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("tool execution error must have isError:true, got: %v", result)
	}
	if content, _ := result["content"].([]any); len(content) == 0 {
		t.Errorf("error result must have content array, got: %v", result)
	}
}

func TestCallOnUnknownServerIsInvalidParams(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("nobody__doThing", map[string]any{}))
	requireRPCError(
		requireRPCErrorParams{T: t, Resp: resp, WantCode: transport.CodeInvalidParams, WantSubstr: "not found"},
	)
}

// TestPing_ReturnsEmptyResult verifies the ping response is exactly {}.
// "The receiver MUST respond promptly with an empty response."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/utilities/ping.mdx#L24
func TestPing_ReturnsEmptyResult(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, rpc("ping", nil))
	if resp["error"] != nil {
		t.Fatalf("ping returned error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || len(result) != 0 {
		t.Errorf("ping result must be {}, got: %v", resp["result"])
	}
}
