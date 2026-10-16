//go:build test

package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestProxy_StableOutput_AllRootShapesMirrorUpstreamText(t *testing.T) {
	cases := []struct {
		name     string
		upstream string
	}{
		{"object root", `{"id":1,"name":"foo"}`},
		{"array root", `[1,2,3]`},
		{"string root", `"hello"`},
		{"number root", `42`},
		{"boolean root", `true`},
		{"null root", `null`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, server.Params{})
			defer srv.Close()
			conn := fakeConn("get_value")
			conn.Responses["tools/call"] = json.RawMessage(
				`{"content":[{"type":"text","text":` + jsonQuote(tc.upstream) + `}]}`,
			)
			addProxyConn(t, srv, "svc", conn)

			resp := serveProxy(t, srv, callTool("svc__get_value", map[string]any{}))
			result := resp["result"].(map[string]any)

			assertDeepEqual(
				t,
				result,
				map[string]any{"content": []any{map[string]any{"type": "text", "text": tc.upstream}}},
			)
		})
	}
}

func TestProxy_StableOutput_StructuredContentIsNotAddedOrDuplicated(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("get_value")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`,
	)
	addProxyConn(t, srv, "svc", conn)

	resp := serveProxy(t, srv, callTool("svc__get_value", map[string]any{}))
	result := resp["result"].(map[string]any)
	if _, ok := result["structuredContent"]; ok {
		t.Errorf("proxy mode must not add structuredContent the upstream did not send: %v", result)
	}
}

func TestProxy_StableOutput_MiniOmittedWhenUnaltered(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("get_value")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	resp := serveProxy(t, srv, callTool("svc__get_value", map[string]any{}))
	text := toolResultText(t, resp)
	if bytesContainsMini(text) {
		t.Errorf("expected no __mini key when projection left data unaltered, got: %s", text)
	}
}

func TestProxy_ErrorBehavior_ToolErrorNotWrappedInData(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("failing_tool")
	conn.Responses["tools/call"] = json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"boom"}]}`)
	addProxyConn(t, srv, "svc", conn)

	resp := serveProxy(t, srv, callTool("svc__failing_tool", map[string]any{}))
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result envelope, got: %v", resp)
	}
	if result["isError"] != true {
		t.Fatalf("expected isError:true for tool failure, got: %v", result)
	}
	text := toolResultText(t, resp)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		if _, hasData := parsed["data"]; hasData {
			t.Errorf("tool error must not be wrapped in \"data\", got: %s", text)
		}
	}
}

// Registry lookup fails before any envelope is built, so this surfaces as a JSON-RPC error, not an isError tool result.
func TestProxy_ErrorBehavior_UnknownServerReturnsToolError(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	resp := serveProxy(t, srv, callTool("nosuchserver__tool", map[string]any{}))
	if resp["error"] == nil {
		result, ok := resp["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Errorf("expected RPC error or isError:true for unknown server, got: %v", resp)
		}
	}
}

func TestProxy_FlatArguments_ReachUpstreamAsSent(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("list_repos")
	addProxyConn(t, srv, "gh", conn)

	resp := serveProxy(t, srv, callTool("gh__list_repos", map[string]any{"state": "open"}))
	if resp["error"] != nil {
		t.Fatalf("flat call should succeed, got: %v", resp["error"])
	}
	if !strings.Contains(string(conn.LastParams), `"state":"open"`) {
		t.Errorf("flat argument not forwarded to upstream: %s", conn.LastParams)
	}
}

func TestProxy_ControlKey_NotForwardedToUpstream(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("get_item")
	addProxyConn(t, srv, "svc", conn)

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{
		"id":     "x",
		"__mini": map[string]any{"projection": "raw"},
	}))
	if resp["error"] != nil || !strings.Contains(string(conn.LastParams), `"id":"x"`) {
		t.Fatalf("call didn't reach the upstream: resp %v, params %s", resp, conn.LastParams)
	}
	if strings.Contains(string(conn.LastParams), "__mini") {
		t.Errorf("__mini must not reach the upstream: %s", conn.LastParams)
	}
}

func TestProxy_ArgsKey_ForwardedAsUpstreamArgument(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("get_item")
	addProxyConn(t, srv, "svc", conn)

	serveProxy(t, srv, callTool("svc__get_item", map[string]any{"args": map[string]any{"id": "x"}}))
	if !strings.Contains(string(conn.LastParams), `"args":{"id":"x"}`) {
		t.Errorf("args key should be forwarded as an upstream argument: %s", conn.LastParams)
	}
}

func TestProxy_ClosedRefRoot_FlatCallWithControlReachesUpstream(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{
				Name:        "get_item",
				Description: "desc",
				InputSchema: json.RawMessage(
					`{"$ref":"#/$defs/Input","$defs":{"Input":{"type":"object","properties":{"id":{"type":"string"}},"additionalProperties":false}}}`,
				),
			},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`),
		},
	}
	addProxyConn(t, srv, "svc", conn)

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{
		"id":     "x",
		"__mini": map[string]any{"projection": "raw"},
	}))
	if resp["error"] != nil {
		t.Fatalf("flat call to closed $ref root should succeed, got: %v", resp["error"])
	}
	if !strings.Contains(string(conn.LastParams), `"id":"x"`) || strings.Contains(string(conn.LastParams), "__mini") {
		t.Errorf("expected only id forwarded, got: %s", conn.LastParams)
	}
}

func TestProxy_Alias_DispatchesToRealUpstreamName(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	fake := fakeConn("list_pull_requests")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"[]"}]}`)
	proj := map[string]*config.ProjectionConfig{
		"list_pull_requests": {Alias: "list_prs"},
	}
	addTestConnection(t, srv, config.ServerConfig{Name: "gh", Projections: proj}, fake)

	resp := serveProxy(t, srv, callTool("gh__list_prs", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("alias response: %s", text)

	if got := lastCalledTool(t, fake); got != "list_pull_requests" {
		t.Errorf("proxy alias call should dispatch upstream using real tool name, got %q", got)
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func bytesContainsMini(s string) bool {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return false
	}
	_, ok := parsed["__mini"]
	return ok
}

func TestProxy_RawBypass_PreservesLargeIntegers(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":9007199254740993}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "raw"}}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "9007199254740993") {
		t.Errorf("raw bypass corrupted large integer: %s", text)
	}
}

func TestProxy_ForwardsLargeIntegerArgs(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serveProxy(
		t,
		srv,
		callTool("svc__get_item", map[string]any{"id": json.Number("9007199254740993")}),
	)
	lastParams := string(conn.LastParams)
	if !strings.Contains(lastParams, "9007199254740993") {
		t.Errorf("large integer corrupted in forwarded args: %s", lastParams)
	}
}

func assertDeepEqual(t *testing.T, got, want any) {
	t.Helper()
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("data mismatch: got %s, want %s", gotJSON, wantJSON)
	}
}
