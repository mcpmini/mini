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

type addProxyConnParams struct {
	T          *testing.T
	Server     *server.Server
	Name       string
	Connection *transport.FakeConnection
}

func addProxyConn(params addProxyConnParams) {
	params.T.Helper()
	writeTestServerConfig(params.T, params.Server, config.ServerConfig{Name: params.Name})
	if err := params.Server.AddConnection(
		context.Background(),
		config.ServerConfig{Name: params.Name},
		params.Connection,
	); err != nil {
		params.T.Fatalf("AddConnection %s: %v", params.Name, err)
	}
}

func TestProxy_Initialize_Instructions(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	msgs := serveAll(t, srv)
	var initResult map[string]any
	for _, m := range msgs {
		if res, ok := m["result"].(map[string]any); ok {
			if _, hasProto := res["protocolVersion"]; hasProto {
				initResult = res
			}
		}
	}
	if initResult == nil {
		t.Fatal("no initialize result")
	}
	const want = "Results are the upstream server's own, trimmed where large: a trimmed result carries `__mini` " +
		"(a key in the result object, the last element of a result list, or a final text block) with a `file` and the paths that were cut. " +
		"To get cut data, call `read` with that `file` and a listed path as `filter`, or a jq filter " +
		"built on it. Pass `__mini: {\"projection\": \"raw\"}` only from a script that must process the " +
		"whole result without a second call. Tool arguments are the upstream server's own; `__mini` is " +
		"reserved."
	if got := initResult["instructions"].(string); got != want {
		t.Errorf("proxy instructions = %q, want %q", got, want)
	}
}

func TestProxy_Call_NoProjection_PassesRawJSON(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	resp := serve(t, srv, callTool("svc__get_user", map[string]any{}))
	text := toolResultText(t, resp)

	if strings.Contains(text, `"__mini"`) {
		t.Errorf("expected raw proxy without __mini envelope: %s", text)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Errorf("expected valid JSON proxy: %s", text)
	}
}

func TestProxy_Call_NoProjection_DefaultStringLimitApplies(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DefaultStringLimit = 10
	srv := newTestServer(t, server.Params{Config: cfg})
	defer srv.Close()

	conn := fakeConn("get_item")
	longVal := strings.Repeat("x", 80)
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"body\":\"` + longVal + `\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	resp := serve(t, srv, callTool("svc__get_item", map[string]any{}))
	text := toolResultText(t, resp)

	if strings.Contains(text, longVal) {
		t.Errorf("expected DefaultStringLimit to truncate long string, but got full value in: %s", text)
	}
}

func TestProxy_Call_WithProjection_ElisionInlinesPlusFile(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("list_repos")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "gh", Connection: conn})

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "gh",
		"tool":       "list_repos",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp := serve(t, srv, callTool("gh__list_repos", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("proxy exclusion response: %s", text)

	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("expected JSON envelope, got: %s", text)
	}
	mini, _ := env["__mini"].(map[string]any)
	if mini == nil {
		t.Fatalf("expected __mini key in envelope: %s", text)
	}
	if excluded, _ := mini["excluded"].([]any); len(excluded) == 0 {
		t.Errorf("expected __mini.excluded to list excluded paths: %s", text)
	}
	if msg, _ := mini["msg"].(string); msg == "" {
		t.Errorf("expected __mini.msg to be set: %s", text)
	}
	if _, hasID := env["id"]; !hasID {
		t.Errorf("expected id in the upstream object, got: %v", env)
	}
	if _, hasSecret := env["secret"]; hasSecret {
		t.Errorf("expected secret to be excluded from the upstream object, got: %v", env)
	}
}

func TestProxy_NestedExclusion_ReportsExactLeafPath(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("list_prs")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"item\":{\"id\":1,\"body\":\"long body text here\"}}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "gh", Connection: conn})

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "gh",
		"tool":       "list_prs",
		"projection": map[string]any{"exclude": []string{"item.body"}},
	}))

	resp := serve(t, srv, callTool("gh__list_prs", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("proxy nested-exclude response: %s", text)

	env := parseProxyEnvelope(t, text)
	if !env.HasMini {
		t.Fatal("expected __mini envelope for nested exclusion")
	}
	if !strings.Contains(text, ".item.body") {
		t.Errorf("expected excluded path .item.body in __mini envelope, got: %s", text)
	}
	if item, ok := env.Data["item"].(map[string]any); !ok || item["id"] != float64(1) || item["body"] != nil {
		t.Errorf("expected item identity without body, got: %v", env.Data)
	}
	if env.File == "" {
		t.Errorf("expected __mini.file for nested exclusion (raw file must be written), got: %s", text)
	}
}

func TestProxy_IncludeFilter_PassthroughWhenAllFieldsIncluded(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("get_data")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"value\":\"data\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_data",
		"projection": map[string]any{"include_only": []string{"id", "value"}},
	}))

	resp := serve(t, srv, callTool("svc__get_data", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("include-filter no-exclusion response: %s", text)

	if strings.Contains(text, `"__mini"`) {
		t.Errorf("expected no __mini envelope when nothing excluded: %s", text)
	}
	env := parseProxyEnvelope(t, text)
	if id, _ := env.Data["id"].(float64); id != 1 {
		t.Errorf("expected id:1, got %v", env.Data["id"])
	}
	if val, _ := env.Data["value"].(string); val != "data" {
		t.Errorf("expected value:data, got %v", env.Data["value"])
	}
}

func TestProxy_Call_WithTruncation_ProjectionNote(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("get_issue")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"body\":\"this is a very long body that will be truncated\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "gh", Connection: conn})

	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection",
		"server": "gh",
		"tool":   "get_issue",
		"projection": map[string]any{
			"string_limits": map[string]any{"body": 5},
		},
	}))

	resp := serve(t, srv, callTool("gh__get_issue", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("truncation note response: %s", text)

	var envelope map[string]any
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("expected JSON envelope: %s", text)
	}
	mini, _ := envelope["__mini"].(map[string]any)
	if mini == nil {
		t.Fatalf("expected __mini in truncation envelope: %s", text)
	}
	truncated, _ := mini["truncated"].([]any)
	if len(truncated) == 0 {
		t.Fatalf("expected __mini.truncated to be non-empty: %s", text)
	}
	entry, _ := truncated[0].(map[string]any)
	if path, _ := entry["path"].(string); path != ".content[0].text | fromjson | .body" {
		t.Errorf("expected truncated[0].path = .content[0].text | fromjson | .body, got %q", path)
	}
	if chars, _ := entry["chars"].(float64); chars == 0 {
		t.Errorf("expected truncated[0].chars > 0: %s", text)
	}
	if msg, _ := mini["msg"].(string); msg == "" {
		t.Errorf("expected __mini.msg to be set: %s", text)
	}
}

func TestProxy_Call_WithExclusionAndTruncation(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("get_issue")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\",\"body\":\"this is a very long body that will be truncated by the limit\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "gh", Connection: conn})

	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection",
		"server": "gh",
		"tool":   "get_issue",
		"projection": map[string]any{
			"exclude":       []string{"secret"},
			"string_limits": map[string]any{"body": 5},
		},
	}))

	resp := serve(t, srv, callTool("gh__get_issue", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("exclusion+truncation response: %s", text)

	var envelope map[string]any
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("expected JSON envelope: %s", text)
	}
	mini, _ := envelope["__mini"].(map[string]any)
	if mini == nil {
		t.Fatalf("expected __mini envelope: %s", text)
	}
	if excluded, _ := mini["excluded"].([]any); len(excluded) == 0 {
		t.Errorf("expected __mini.excluded when field excluded: %s", text)
	}
	if truncated, _ := mini["truncated"].([]any); len(truncated) == 0 {
		t.Errorf("expected __mini.truncated when string truncated: %s", text)
	}
	if msg, _ := mini["msg"].(string); msg == "" {
		t.Errorf("expected __mini.msg to be set: %s", text)
	}
}

func TestProxy_Call_ToolFormatToon_RendersToon(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	serve(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_user",
		"projection": map[string]any{"format": "toon"},
	}))

	resp := serve(t, srv, callTool("svc__get_user", map[string]any{}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") {
		t.Errorf("per-tool format:toon should produce TOON, got JSON: %s", text)
	}
	if !strings.Contains(text, "name: alice") {
		t.Errorf("expected name field in TOON output, got: %s", text)
	}
}

func TestProxy_Call_GlobalFormatToon_RendersToonUnlessTheToolSaysJSON(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseFormat = "toon"
	srv := newTestServer(t, server.Params{Config: cfg})
	defer srv.Close()

	conn := fakeConn("get_user", "get_raw_user")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "get_raw_user",
		"projection": map[string]any{"format": "json"},
	}))

	text := toolResultText(t, serve(t, srv, callTool("svc__get_user", map[string]any{})))
	if strings.HasPrefix(text, "{") || !strings.Contains(text, "name: alice") {
		t.Errorf("global response_format:toon should produce TOON in proxy mode, got: %s", text)
	}
	text = toolResultText(t, serve(t, srv, callTool("svc__get_raw_user", map[string]any{})))
	if !strings.HasPrefix(text, "{") {
		t.Errorf("per-tool format:json should override global toon, got: %s", text)
	}
}

func TestProxy_Call_SessionFormatToon_RendersToon(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	conn := fakeConn("list_items")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"[{\"id\":1,\"name\":\"foo\"},{\"id\":2,\"name\":\"bar\"}]"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	const sessionID = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
	postMCP(postMCPParams{T: t, Server: srv, SessionID: sessionID, Msg: initMsg()})

	postMCP(postMCPParams{
		T: t, Server: srv, SessionID: sessionID, Msg: map[string]any{
			"jsonrpc": transport.JsonRPCVersion, "id": 1, "method": "tools/call",
			"params": map[string]any{
				"name": "config",
				"arguments": map[string]any{
					"action": "set_projection", "server": "svc", "tool": "list_items",
					"projection": map[string]any{"format": "toon"}, "session_only": true,
				},
			},
		},
	})

	resp := postMCP(postMCPParams{
		T: t, Server: srv, SessionID: sessionID, Msg: map[string]any{
			"jsonrpc": transport.JsonRPCVersion, "id": 2, "method": "tools/call",
			"params": map[string]any{"name": "svc__list_items", "arguments": map[string]any{}},
		},
	})

	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("no content in response")
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if strings.HasPrefix(text, "{") {
		t.Errorf("session format:toon should produce TOON in proxy mode, got JSON: %s", text)
	}
}
