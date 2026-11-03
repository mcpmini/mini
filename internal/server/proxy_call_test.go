//go:build test

package server_test

import (
	"context"
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
			h := newProxyHarness(t, newTestServer(t, server.Params{}))
			h.addUpstream("svc", textResult(tc.upstream), "get_value")

			result := h.call("svc__get_value", nil)

			assertDeepEqual(
				t,
				result,
				map[string]any{"content": []any{map[string]any{"type": "text", "text": tc.upstream}}},
			)
		})
	}
}

func TestProxy_StableOutput_StructuredContentIsNotAddedOrDuplicated(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(`{"id":1,"name":"alice"}`), "get_value")

	result := h.call("svc__get_value", nil)

	if _, ok := result["structuredContent"]; ok {
		t.Errorf("proxy mode must not add structuredContent the upstream did not send: %v", result)
	}
}

func TestProxy_StableOutput_MiniOmittedWhenUnaltered(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(`{"id":1}`), "get_value")

	text := h.callText("svc__get_value", nil)

	if bytesContainsMini(text) {
		t.Errorf("expected no __mini key when projection left data unaltered, got: %s", text)
	}
}

func TestProxy_ErrorBehavior_ToolErrorNotWrappedInData(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", `{"isError":true,"content":[{"type":"text","text":"boom"}]}`, "failing_tool")

	result := h.call("svc__failing_tool", nil)

	if result["isError"] != true {
		t.Fatalf("expected isError:true for tool failure, got: %v", result)
	}
	texts := contentTexts(result)
	if len(texts) != 1 {
		t.Fatalf("expected one text block in the tool error, got: %v", result)
	}
	text := texts[0]
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		if _, hasData := parsed["data"]; hasData {
			t.Errorf("tool error must not be wrapped in \"data\", got: %s", text)
		}
	}
}

func TestProxy_ErrorBehavior_UnknownServerReturnsRPCError(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("nosuchserver__tool", map[string]any{}))
	requireRPCError(requireRPCErrorParams{
		T: t, Resp: resp, WantCode: transport.CodeInvalidParams, WantSubstr: "not found",
	})
	if resp["result"] != nil {
		t.Fatalf("unknown server response = %v, want no tool result", resp)
	}
}

func TestProxy_FlatArguments_ReachUpstreamAsSent(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("list_repos")
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "gh", Connection: conn})

	resp := serve(t, srv, callTool("gh__list_repos", map[string]any{"state": "open"}))
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
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	resp := serve(t, srv, callTool("svc__get_item", map[string]any{
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
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	serve(t, srv, callTool("svc__get_item", map[string]any{"args": map[string]any{"id": "x"}}))
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
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	resp := serve(t, srv, callTool("svc__get_item", map[string]any{
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
	addTestConnection(
		addConnectionParams{
			T:          t,
			Server:     srv,
			Config:     config.ServerConfig{Name: "gh", Projections: proj},
			Connection: fake,
		},
	)

	resp := serve(t, srv, callTool("gh__list_prs", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("alias response: %s", text)

	if got := lastCalledTool(t, fake); got != "list_pull_requests" {
		t.Errorf("proxy alias call should dispatch upstream using real tool name, got %q", got)
	}
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
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", textResult(`{"id":9007199254740993}`), "get_item")

	text := h.callText("svc__get_item", map[string]any{"__mini": map[string]any{"projection": "raw"}})

	if !strings.Contains(text, "9007199254740993") {
		t.Errorf("raw bypass corrupted large integer: %s", text)
	}
}

func TestProxy_ForwardsLargeIntegerArgs(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	conn := h.addUpstream("svc", textResult(`{}`), "get_item")

	h.call("svc__get_item", map[string]any{"id": json.Number("9007199254740993")})

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

func TestExecWithFakeConnectionError(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fakeConnWithError("ping"))

	resp := serve(t, srv, callTool("svc__ping", map[string]any{}))
	requireToolError(t, resp, "ok")
}

func TestExecuteReturnsEnvelope(t *testing.T) {
	srv, fake := newPipelineServer(t, "getBuild",
		`{"content":[{"type":"text","text":"{\"build_number\":1234,\"status\":\"failed\",\"branch\":\"main\"}"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "ci"}, fake)
	resp := serve(t, srv, callTool("ci__getBuild", map[string]any{"id": "1234"}))
	verifyEnvelope(t, successText(t, resp))
}

func TestExecuteRoutesToUpstream(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	fake := fakeConn("getBuild")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"build ok"}]}`)
	addTestConnection(addConnectionParams{T: t, Server: srv, Config: config.ServerConfig{Name: "ci"}, Connection: fake})

	resp := serve(t, srv, callTool("ci__getBuild", map[string]any{"id": "123"}))

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
}

// verifyEnvelope checks the envelope structure returned by execute.
func verifyEnvelope(t *testing.T, text string) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("execute result is not JSON: %v\n%s", err, text)
	}
	return env
}

func newPipelineServer(t *testing.T, toolName string, response string) (*server.Server, *transport.FakeConnection) {
	t.Helper()
	srv := newTestServer(t, server.Params{})
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: toolName, Description: toolName, InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{"tools/call": json.RawMessage(response)},
	}
	return srv, fake
}
