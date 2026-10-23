//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"reflect"
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

func toolsList(t *testing.T, srv *server.Server) []map[string]any {
	t.Helper()
	msgs := serveAll(t, srv, rpc("tools/list", nil))
	for _, m := range msgs {
		if res, ok := m["result"].(map[string]any); ok {
			if tools, ok := res["tools"].([]any); ok {
				out := make([]map[string]any, len(tools))
				for i, tool := range tools {
					out[i] = tool.(map[string]any)
				}
				return out
			}
		}
	}
	t.Fatal("no tools/list result found")
	return nil
}

func toolNames(tools []map[string]any) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t["name"].(string)
	}
	return names
}

func containsName(tools []map[string]any, name string) bool {
	for _, t := range tools {
		if t["name"] == name {
			return true
		}
	}
	return false
}

func TestProxy_ToolsList_ContainsMiniTools(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("list_issues", "create_issue")
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "github", Connection: conn})

	tools := toolsList(t, srv)
	names := toolNames(tools)
	t.Logf("tools: %v", names)

	if !containsName(tools, "config") {
		t.Error("expected config in proxy tool list")
	}
	if !containsName(tools, "read") {
		t.Error("expected read in proxy tool list")
	}
	if !containsName(tools, "github__list_issues") {
		t.Error("expected github__list_issues in proxy tool list")
	}
	if !containsName(tools, "github__create_issue") {
		t.Error("expected github__create_issue in proxy tool list")
	}
}

func TestProxy_ToolsList_MiniToolsNoMeta(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()

	tools := toolsList(t, srv)
	for _, tool := range tools {
		name := tool["name"].(string)
		if name == "config" || name == "read" {
			if tool["_meta"] != nil {
				t.Errorf("%s: expected no _meta, got %v", name, tool["_meta"])
			}
		}
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
		"(a key in the result object, or a final text block) with a `file` and the paths that were cut. " +
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

func TestProxy_Call_GlobalFormatToon_RendersToon(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseFormat = "toon"
	srv := newTestServer(t, server.Params{Config: cfg})
	defer srv.Close()

	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(
		`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`,
	)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})

	resp := serve(t, srv, callTool("svc__get_user", map[string]any{}))
	text := toolResultText(t, resp)
	if strings.HasPrefix(text, "{") || !strings.Contains(text, "name: alice") {
		t.Errorf("global response_format:toon should produce TOON in proxy mode, got: %s", text)
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

func TestProxy_MiniConfig_Status_Works(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("list_issues")
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "github", Connection: conn})

	resp := serve(t, srv, callTool("config", map[string]any{"action": "status"}))
	text := toolResultText(t, resp)
	t.Logf("status: %s", text)

	var status map[string]any
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("status should be JSON: %s", text)
	}
	if status["servers"] == nil {
		t.Error("expected servers in status")
	}
}

func TestProxy_NotifyAll_OnRemoveServer(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("list_issues")
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "removeme", Connection: conn})

	msgs := serveAll(t, srv,
		notification(transport.NotificationInitialized, nil),
		callTool("config", map[string]any{
			"action": "remove_server",
			"server": "removeme",
		}),
	)

	if !hasNotification(msgs, transport.NotificationToolsChanged) {
		t.Error("expected notifications/tools/list_changed after remove_server in proxy mode")
	}
}

func fakeConnWithAnnotations(name string, annotations json.RawMessage) *transport.FakeConnection {
	return &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{
				Name:        name,
				Description: "desc for " + name,
				InputSchema: json.RawMessage(`{"type":"object"}`),
				Annotations: annotations,
			},
		},
		Responses: make(map[string]json.RawMessage),
	}
}

func findTool(tools []map[string]any, name string) map[string]any {
	for _, t := range tools {
		if t["name"] == name {
			return t
		}
	}
	return nil
}

func assertAnnotationsEqual(t *testing.T, got any, want json.RawMessage) {
	t.Helper()
	gotRaw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got annotations: %v", err)
	}
	var gotVal, wantVal any
	if err := json.Unmarshal(gotRaw, &gotVal); err != nil {
		t.Fatalf("unmarshal got annotations: %v", err)
	}
	if err := json.Unmarshal(want, &wantVal); err != nil {
		t.Fatalf("unmarshal want annotations: %v", err)
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Errorf("annotations mismatch: got %s, want %s", gotRaw, want)
	}
}

func TestProxy_ToolsList_AnnotationsPassthrough(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	raw := json.RawMessage(`{"readOnlyHint":true}`)
	conn := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{
			Name:         "get_file",
			Description:  "desc for get_file",
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			Annotations:  raw,
			Title:        json.RawMessage(`"My Tool"`),
			OutputSchema: json.RawMessage(`{"type":"string"}`),
			Meta:         json.RawMessage(`{"key":"val"}`),
			Icons:        json.RawMessage(`{"url":"http://example.com/icon.png"}`),
			Execution:    json.RawMessage(`{"timeout":30}`),
		}},
		Responses: make(map[string]json.RawMessage),
	}
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "fs", Connection: conn})

	tools := toolsList(t, srv)
	tool := findTool(tools, "fs__get_file")
	if tool == nil {
		t.Fatal("fs__get_file not found in tools/list")
	}
	assertAnnotationsEqual(t, tool["annotations"], raw)
	for _, key := range []string{"title", "outputSchema", "_meta", "icons", "execution"} {
		if tool[key] == nil {
			t.Errorf("expected %q in tool definition, got nil", key)
		}
	}
}

func TestProxy_ToolsList_MultiKeyAnnotationsPassthrough(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	raw := json.RawMessage(
		`{"readOnlyHint":true,"destructiveHint":false,"idempotentHint":true,"openWorldHint":false,"title":"Get File","fakeHint":true}`,
	)
	conn := fakeConnWithAnnotations("get_file", raw)
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "fs2", Connection: conn})

	tools := toolsList(t, srv)
	tool := findTool(tools, "fs2__get_file")
	if tool == nil {
		t.Fatal("fs2__get_file not found in tools/list")
	}
	assertAnnotationsEqual(t, tool["annotations"], raw)
}

func TestProxy_ToolsList_AbsentAnnotationsOmitted(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("write_file")
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "fs", Connection: conn})

	tools := toolsList(t, srv)
	tool := findTool(tools, "fs__write_file")
	if tool == nil {
		t.Fatal("fs__write_file not found in tools/list")
	}

	for _, key := range []string{"annotations", "title", "_meta", "icons", "execution"} {
		if _, ok := tool[key]; ok {
			t.Errorf("tool without %q must not include that key, got: %v", key, tool[key])
		}
	}
}

func TestProxy_ToolsList_OutputSchemaOmittedWhenUpstreamHasNone(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	defer srv.Close()
	conn := fakeConn("write_file")
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "fs", Connection: conn})

	tools := toolsList(t, srv)
	tool := findTool(tools, "fs__write_file")
	if tool == nil {
		t.Fatal("fs__write_file not found in tools/list")
	}
	if _, ok := tool["outputSchema"]; ok {
		t.Errorf("tool without an upstream outputSchema must not advertise one, got: %v", tool["outputSchema"])
	}
}
