//go:build test

package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestProxy_StableOutput_AllRootShapesWrapInData(t *testing.T) {
	cases := []struct {
		name     string
		upstream string
		wantData any
	}{
		{"object root", `{"id":1,"name":"foo"}`, map[string]any{"id": float64(1), "name": "foo"}},
		{"array root", `[1,2,3]`, []any{float64(1), float64(2), float64(3)}},
		{"string root", `"hello"`, "hello"},
		{"number root", `42`, float64(42)},
		{"boolean root", `true`, true},
		{"null root", `null`, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newProxyServer(t)
			defer srv.Close()
			conn := fakeConn("get_value")
			conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":` + jsonQuote(tc.upstream) + `}]}`)
			addProxyConn(t, srv, "svc", conn)

			resp := serveProxy(t, srv, callTool("svc__get_value", map[string]any{}))
			text := toolResultText(t, resp)

			var wrapper struct {
				Data any `json:"data"`
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(text), &raw); err != nil {
				t.Fatalf("expected JSON object envelope, got: %s", text)
			}
			if _, ok := raw["data"]; !ok {
				t.Fatalf("expected top-level \"data\" key even for %s, got: %s", tc.name, text)
			}
			if err := json.Unmarshal([]byte(text), &wrapper); err != nil {
				t.Fatalf("unmarshal envelope: %v", err)
			}
			assertDeepEqual(t, wrapper.Data, tc.wantData)
		})
	}
}

func TestProxy_StableOutput_TextAndStructuredContentMatch(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("get_value")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	resp := serveProxy(t, srv, callTool("svc__get_value", map[string]any{}))
	result := resp["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	structured, ok := result["structuredContent"]
	if !ok {
		t.Fatal("expected structuredContent in proxy result")
	}
	structuredBytes, _ := json.Marshal(structured)
	var fromText, fromStructured any
	json.Unmarshal([]byte(text), &fromText)
	json.Unmarshal(structuredBytes, &fromStructured)
	assertDeepEqual(t, fromText, fromStructured)
}

func TestProxy_StableOutput_MiniOmittedWhenUnaltered(t *testing.T) {
	srv := newProxyServer(t)
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
	srv := newProxyServer(t)
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

// Registry lookup for an unknown server__tool fails before any envelope is
// built, so this surfaces as a JSON-RPC error, not an isError tool result —
// accept either, matching TestProxy_UnknownTool_ReturnsError.
func TestProxy_ErrorBehavior_UnknownServerReturnsToolError(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	resp := serveProxy(t, srv, callTool("nosuchserver__tool", map[string]any{}))
	if resp["error"] == nil {
		result, ok := resp["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Errorf("expected RPC error or isError:true for unknown server, got: %v", resp)
		}
	}
}

func TestProxy_LegacyFlatCall_RejectedWithActionableError(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("list_repos")
	addProxyConn(t, srv, "gh", conn)

	resp := serveProxy(t, srv, callTool("gh__list_repos", map[string]any{"state": "open"}))
	requireRPCError(t, resp, -32602, "args")
}

func TestProxy_Action_DispatchesToRealUpstreamTool(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	fake := fakeConn("list_pull_requests")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"[]"}]}`)
	addProxyConn(t, srv, "gh", fake)
	srv.RegisterAction(config.ActionConfig{
		Name:        "my_prs",
		Description: "My open PRs",
		Server:      "gh",
		Tool:        "list_pull_requests",
		DefaultArgs: map[string]any{"state": "open", "author": "me"},
	})

	resp := serveProxy(t, srv, callTool("gh__my_prs", map[string]any{"args": map[string]any{"state": "closed"}}))
	text := toolResultText(t, resp)
	t.Logf("action response: %s", text)

	assertUpstreamArgs(t, fake, map[string]any{"state": "closed", "author": "me"})
	if got := lastCalledTool(t, fake); got != "list_pull_requests" {
		t.Errorf("action should dispatch to real upstream tool, got %q", got)
	}
}

func TestProxy_Alias_DispatchesToRealUpstreamName(t *testing.T) {
	srv := newProxyServer(t)
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
	srv := newProxyServer(t)
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
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("get_item")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serveProxy(t, srv, callTool("svc__get_item", map[string]any{"args": map[string]any{"id": json.Number("9007199254740993")}}))
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
