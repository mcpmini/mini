//go:build test

// Package server_test — MCP wire-format compliance tests.
//
// These tests verify the exact JSON shape of every message type mini sends,
// checked against the spec schema definitions. Each test is annotated with the
// spec section it covers so failures point directly to the relevant requirement.
//
// Spec version: 2025-03-26
// Spec repo: https://github.com/modelcontextprotocol/modelcontextprotocol
// Schema: schema/2025-03-26/schema.json
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

// --- helpers -----------------------------------------------------------------

func initializeResult(t *testing.T) map[string]any {
	t.Helper()
	srv := newTestServer(t)
	resp := serve(t, srv, rpc("initialize", map[string]any{
		"protocolVersion": transport.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}))
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize result missing: %v", resp)
	}
	return result
}

func callToolResult(t *testing.T, toolName string, args map[string]any) map[string]any {
	t.Helper()
	srv := newTestServer(t)
	fake := fakeConn(toolName)
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": toolName, "params": args,
	}))
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got: %v", resp)
	}
	return result
}

// --- InitializeResult shape --------------------------------------------------

// TestSchemaCompliance_InitializeResult verifies the InitializeResult has all required fields.
// Spec §Initialization: "The server MUST respond with its own capabilities and information."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L79
func TestSchemaCompliance_InitializeResult(t *testing.T) {
	result := initializeResult(t)

	t.Run("protocolVersion present", func(t *testing.T) {
		v, _ := result["protocolVersion"].(string)
		if v == "" {
			t.Errorf("protocolVersion must be a non-empty string, got: %v", result["protocolVersion"])
		}
	})

	t.Run("capabilities present", func(t *testing.T) {
		caps, ok := result["capabilities"].(map[string]any)
		if !ok {
			t.Fatalf("capabilities must be an object, got: %T %v", result["capabilities"], result["capabilities"])
		}
		// tools capability MUST be declared when server exposes tools
		// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L36
		if _, ok := caps["tools"]; !ok {
			t.Errorf("capabilities.tools must be present, got: %v", caps)
		}
	})

	t.Run("capabilities.tools.listChanged declared", func(t *testing.T) {
		// Server declares listChanged:true, so it MUST send tools/list_changed when tool set changes.
		// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L47
		caps, _ := result["capabilities"].(map[string]any)
		tools, _ := caps["tools"].(map[string]any)
		if lc, _ := tools["listChanged"].(bool); !lc {
			t.Errorf("capabilities.tools.listChanged must be true, got: %v", tools)
		}
	})

	t.Run("serverInfo present", func(t *testing.T) {
		info, ok := result["serverInfo"].(map[string]any)
		if !ok {
			t.Fatalf("serverInfo must be an object, got: %T", result["serverInfo"])
		}
		if name, _ := info["name"].(string); name == "" {
			t.Errorf("serverInfo.name must be non-empty")
		}
		if ver, _ := info["version"].(string); ver == "" {
			t.Errorf("serverInfo.version must be non-empty")
		}
	})
}

// --- ListToolsResult shape ---------------------------------------------------

// TestSchemaCompliance_ToolsList verifies tools/list response shape.
// Spec §Listing Tools: result must have a "tools" array; each Tool has name + inputSchema.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L55
func TestSchemaCompliance_ToolsList(t *testing.T) {
	srv := newTestServer(t)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fakeConn("doThing"))

	resp := serve(t, srv, rpc("tools/list", nil))
	if resp["error"] != nil {
		t.Fatalf("tools/list returned error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result must be object, got: %v", resp)
	}

	t.Run("tools array present", func(t *testing.T) {
		if _, ok := result["tools"].([]any); !ok {
			t.Errorf("tools must be an array, got: %T", result["tools"])
		}
	})

	t.Run("no extra required fields", func(t *testing.T) {
		// nextCursor is optional — absence is fine when all results fit one page
		// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/utilities/pagination.mdx#L23
		_ = result["nextCursor"] // may be absent
	})

	t.Run("jsonrpc 2.0 envelope", func(t *testing.T) {
		// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx
		if v, _ := resp["jsonrpc"].(string); v != "2.0" {
			t.Errorf("jsonrpc must be \"2.0\", got %q", v)
		}
		if resp["id"] == nil {
			t.Errorf("response id must be present (echoing request id)")
		}
	})
}

// --- CallToolResult shape ----------------------------------------------------

// TestSchemaCompliance_CallToolResult verifies the successful tool result shape.
// Spec: result must have a "content" array with typed content items.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L120
func TestSchemaCompliance_CallToolResult(t *testing.T) {
	result := callToolResult(t, "myTool", map[string]any{})

	t.Run("content array present", func(t *testing.T) {
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Errorf("content must be a non-empty array, got: %v", result["content"])
		}
	})

	t.Run("content items have type", func(t *testing.T) {
		content, _ := result["content"].([]any)
		for i, item := range content {
			m, ok := item.(map[string]any)
			if !ok {
				t.Errorf("content[%d] must be object, got %T", i, item)
				continue
			}
			if _, ok := m["type"].(string); !ok {
				t.Errorf("content[%d].type must be a string, got: %v", i, m["type"])
			}
		}
	})

	t.Run("isError absent on success", func(t *testing.T) {
		// isError should be absent (or false) for success results.
		// JSON omitempty drops false, which is equivalent per the schema.
		if ie := result["isError"]; ie == true {
			t.Errorf("isError must not be true on success, got: %v", ie)
		}
	})
}

// TestSchemaCompliance_ToolErrorResult verifies that tool execution errors use
// isError:true in the result, NOT a JSON-RPC error. This is the spec-required
// distinction between protocol errors and tool execution errors.
// Spec: "Tool errors SHOULD be reported in tool results with isError:true"
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L244
func TestSchemaCompliance_ToolErrorResult(t *testing.T) {
	srv := newTestServer(t)
	fake := fakeConnWithError("errTool")
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "errTool", "params": map[string]any{},
	}))

	t.Run("no JSON-RPC error", func(t *testing.T) {
		if resp["error"] != nil {
			t.Errorf("tool execution error must NOT be a JSON-RPC error, got: %v", resp["error"])
		}
	})

	t.Run("isError true in result", func(t *testing.T) {
		result, _ := resp["result"].(map[string]any)
		if result["isError"] != true {
			t.Errorf("tool execution error must have isError:true, got: %v", result)
		}
	})

	t.Run("content array present", func(t *testing.T) {
		result, _ := resp["result"].(map[string]any)
		content, ok := result["content"].([]any)
		if !ok || len(content) == 0 {
			t.Errorf("error result must have content array, got: %v", result)
		}
	})
}

// TestSchemaCompliance_ProtocolError_vs_ToolError verifies the distinction between
// protocol errors (unknown tool → JSON-RPC error) and tool errors (isError:true).
// "Protocol Errors: Standard JSON-RPC errors for unknown tools, invalid arguments."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L244
func TestSchemaCompliance_ProtocolError_vs_ToolError(t *testing.T) {
	srv := newTestServer(t)

	t.Run("unknown tool is JSON-RPC error not tool error", func(t *testing.T) {
		resp := serve(t, srv, callTool("call", map[string]any{
			"server": "nobody", "tool": "nothing", "params": map[string]any{},
		}))
		if resp["error"] == nil {
			t.Errorf("unknown tool must return JSON-RPC error (not tool-level isError), got: %v", resp)
		}
		if resp["result"] != nil {
			t.Errorf("JSON-RPC error response must not have result, got: %v", resp["result"])
		}
		errObj, _ := resp["error"].(map[string]any)
		code, _ := errObj["code"].(float64)
		if int(code) != transport.CodeInvalidParams {
			t.Errorf("unknown tool error code = %d, want %d", int(code), transport.CodeInvalidParams)
		}
	})
}

// --- Notification shape ------------------------------------------------------

// TestSchemaCompliance_ToolsListChangedNotification verifies the tools/list_changed
// notification has the correct JSON-RPC envelope shape.
// Spec §List Changed Notification: "servers that declared listChanged SHOULD send a notification"
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L139
func TestSchemaCompliance_ToolsListChangedNotification(t *testing.T) {
	srv := newTestServer(t)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "pre"}, fakeConn("existing"))

	msgs := serveAll(t, srv,
		callTool("config", map[string]any{"action": "remove_server", "server": "pre"}),
	)

	var found map[string]any
	for _, m := range msgs {
		if m["method"] == transport.NotificationToolsChanged {
			found = m
			break
		}
	}
	if found == nil {
		t.Fatal("tools/list_changed notification not sent after remove_server")
	}

	t.Run("jsonrpc 2.0", func(t *testing.T) {
		if v, _ := found["jsonrpc"].(string); v != "2.0" {
			t.Errorf("notification jsonrpc must be \"2.0\", got %q", v)
		}
	})

	t.Run("no id field", func(t *testing.T) {
		// Notifications MUST NOT have an id field.
		// https://www.jsonrpc.org/specification#notification
		if found["id"] != nil {
			t.Errorf("notification must not have id, got: %v", found["id"])
		}
	})

	t.Run("correct method", func(t *testing.T) {
		if m, _ := found["method"].(string); m != "notifications/tools/list_changed" {
			t.Errorf("method = %q, want notifications/tools/list_changed", m)
		}
	})
}

// --- JSON-RPC envelope -------------------------------------------------------

// TestSchemaCompliance_RPCEnvelope verifies that all response messages have
// jsonrpc:"2.0" and that result/error are mutually exclusive.
// https://www.jsonrpc.org/specification#response_object
func TestSchemaCompliance_RPCEnvelope(t *testing.T) {
	responses := []struct {
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

	for _, tc := range responses {
		t.Run(tc.name, func(t *testing.T) {
			lines := rawServe(t, newTestServer(t), tc.input)
			if len(lines) == 0 {
				t.Fatal("no response")
			}
			var msg map[string]any
			if err := json.Unmarshal(lines[len(lines)-1], &msg); err != nil {
				t.Fatalf("response not valid JSON: %s", lines[len(lines)-1])
			}
			if v, _ := msg["jsonrpc"].(string); v != "2.0" {
				t.Errorf("jsonrpc must be \"2.0\", got %q", v)
			}
			hasResult := msg["result"] != nil
			hasError := msg["error"] != nil
			if hasResult && hasError {
				t.Errorf("response must not have both result and error")
			}
			if !hasResult && !hasError {
				t.Errorf("response must have either result or error")
			}
		})
	}
}

// TestSchemaCompliance_ErrorObject verifies JSON-RPC error responses have the
// required code (integer) and message (string) fields.
// https://www.jsonrpc.org/specification#error_object
func TestSchemaCompliance_ErrorObject(t *testing.T) {
	srv := newTestServer(t)
	resp := serve(t, srv, rpc("no_such_method", nil))

	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error field, got: %v", resp)
	}

	t.Run("code is integer", func(t *testing.T) {
		code, ok := errObj["code"].(float64)
		if !ok {
			t.Errorf("error.code must be a number, got: %T %v", errObj["code"], errObj["code"])
		}
		if code != float64(int(code)) {
			t.Errorf("error.code must be an integer, got: %v", code)
		}
	})

	t.Run("message is string", func(t *testing.T) {
		msg, ok := errObj["message"].(string)
		if !ok || msg == "" {
			t.Errorf("error.message must be a non-empty string, got: %T %v", errObj["message"], errObj["message"])
		}
	})
}

// TestSchemaCompliance_PingResponse verifies ping returns exactly {}.
// "The receiver MUST respond promptly with an empty response."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/utilities/ping.mdx#L24
func TestSchemaCompliance_PingResponse(t *testing.T) {
	srv := newTestServer(t)
	resp := serve(t, srv, rpc("ping", nil))
	if resp["error"] != nil {
		t.Fatalf("ping returned error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || len(result) != 0 {
		t.Errorf("ping result must be exactly {}, got: %v", resp["result"])
	}
}

// TestSchemaCompliance_ErrorCodes verifies that standard JSON-RPC error codes
// are used correctly for the right conditions.
// https://www.jsonrpc.org/specification#error_object (codes -32700 to -32603)
func TestSchemaCompliance_ErrorCodes(t *testing.T) {
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
			input:    rpc("tools/list", nil), // sent without initialize
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
			var resp map[string]any
			json.Unmarshal(lines[len(lines)-1], &resp) //nolint:errcheck
			errObj, ok := resp["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected error, got: %v", resp)
			}
			got := int(errObj["code"].(float64))
			if got != tc.wantCode {
				t.Errorf("error code = %d, want %d", got, tc.wantCode)
			}
		})
	}
}

// TestSchemaCompliance_NotificationProducesNoID verifies that server-sent
// notifications (tools/list_changed) have no id field, per JSON-RPC spec.
// Notifications are fire-and-forget messages with no id.
// https://www.jsonrpc.org/specification#notification
func TestSchemaCompliance_NotificationProducesNoID(t *testing.T) {
	srv := newTestServer(t)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fakeConn("t"))

	msgs := serveAll(t, srv,
		callTool("config", map[string]any{"action": "remove_server", "server": "svc"}),
	)

	for _, m := range msgs {
		if m["method"] == transport.NotificationToolsChanged {
			if _, hasID := m["id"]; hasID {
				t.Errorf("notification must not have id field, got: %v", m)
			}
			if v, _ := m["jsonrpc"].(string); v != "2.0" {
				t.Errorf("notification jsonrpc must be \"2.0\", got %q", v)
			}
			return
		}
	}
	t.Error("tools/list_changed notification not found")
}

// TestSchemaCompliance_CancelledNotification verifies that notifications/cancelled
// is accepted silently (no response for unknown request IDs).
// "Receivers MAY ignore cancellation notifications if the referenced request is unknown."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/utilities/cancellation.mdx#L33
func TestSchemaCompliance_CancelledNotification(t *testing.T) {
	srv := newTestServer(t)
	msgs := serveAll(t, srv,
		notification("notifications/cancelled", map[string]any{
			"requestId": "999",
			"reason":    "test",
		}),
	)
	for _, m := range msgs {
		if m["id"] == nil {
			continue
		}
		// Any response with an id should NOT be an error triggered by the notification.
		// (The initialize response is fine; any other error is the problem.)
		if m["method"] != nil {
			continue
		}
		id, _ := m["id"].(float64)
		if int(id) == 0 {
			continue // the initialize response
		}
		if m["error"] != nil {
			t.Errorf("notifications/cancelled for unknown id must not produce error response, got: %v", m)
		}
	}
}

// --- Inline content verification ---------------------------------------------

// TestSchemaCompliance_TextContent verifies that text content items have required fields.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L199
func TestSchemaCompliance_TextContent(t *testing.T) {
	result := callToolResult(t, "sayHello", map[string]any{})
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("no content items")
	}
	item, _ := content[0].(map[string]any)

	t.Run("type is text", func(t *testing.T) {
		if typ, _ := item["type"].(string); typ != "text" {
			t.Errorf("text content type = %q, want \"text\"", typ)
		}
	})

	t.Run("text field is string", func(t *testing.T) {
		if _, ok := item["text"].(string); !ok {
			t.Errorf("text content must have string text field, got: %T", item["text"])
		}
	})
}

// TestSchemaCompliance_ResponseIDEchoing verifies that the response id matches
// the request id for both string and integer request IDs.
// JSON-RPC spec: "id MUST be the same as the value of the id member in the Request Object."
// https://www.jsonrpc.org/specification#response_object
func TestSchemaCompliance_ResponseIDEchoing(t *testing.T) {
	for _, id := range []any{42, "req-abc-123", 0} {
		t.Run("", func(t *testing.T) {
			rawID, _ := json.Marshal(id)
			reqBytes, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": json.RawMessage(rawID), "method": "ping",
			})
			reqBytes = append(reqBytes, '\n')

			lines := rawServe(t, newTestServer(t), buildServeInput([][]byte{reqBytes}))
			found := false
			for _, line := range lines {
				var msg map[string]any
				json.Unmarshal(line, &msg) //nolint:errcheck
				// Match by comparing raw JSON of the id field.
				respIDRaw, _ := json.Marshal(msg["id"])
				if bytes.Equal(respIDRaw, rawID) {
					found = true
					if msg["error"] != nil {
						t.Errorf("ping with id=%v returned error: %v", id, msg["error"])
					}
					break
				}
			}
			if !found {
				t.Errorf("no response found with id=%v", id)
			}
		})
	}
}
