//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func newProxyServer(t *testing.T) *server.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	return server.New(cfg, logger)
}

func addProxyConn(t *testing.T, srv *server.Server, name string, conn *transport.FakeConnection) {
	t.Helper()
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: name}, conn); err != nil {
		t.Fatalf("AddConnection %s: %v", name, err)
	}
}

func toolsList(t *testing.T, srv *server.Server) []map[string]any {
	t.Helper()
	msgs := serveAllProxy(t, srv, rpc("tools/list", nil))
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
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("list_issues", "create_issue")
	addProxyConn(t, srv, "github", conn)

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
	srv := newProxyServer(t)
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
	srv := newProxyServer(t)
	defer srv.Close()

	msgs := serveAllProxy(t, srv)
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
	instructions := initResult["instructions"].(string)
	if !strings.Contains(instructions, "read") {
		t.Errorf("proxy instructions should mention read: %q", instructions)
	}
	if strings.Contains(instructions, "perm_call") {
		t.Errorf("proxy instructions should not mention perm_call: %q", instructions)
	}
}

func TestProxy_Call_NoProjection_PassesRawJSON(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	resp := serveProxy(t, srv, callTool("svc__get_user", map[string]any{}))
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
	cfg.ResponseDir = t.TempDir()
	cfg.DefaultStringLimit = 10
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(cfg, logger)
	defer srv.Close()

	conn := fakeConn("get_item")
	longVal := strings.Repeat("x", 80)
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"body\":\"` + longVal + `\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	resp := serveProxy(t, srv, callTool("svc__get_item", map[string]any{}))
	text := toolResultText(t, resp)

	if strings.Contains(text, longVal) {
		t.Errorf("expected DefaultStringLimit to truncate long string, but got full value in: %s", text)
	}
}

func TestProxy_Call_WithProjection_ElisionInlinesPlusFile(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("list_repos")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"hidden\"}"}]}`)
	addProxyConn(t, srv, "gh", conn)

	serveProxy(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "gh",
		"tool":       "list_repos",
		"projection": map[string]any{"exclude": []string{"secret"}},
	}))

	resp := serveProxy(t, srv, callTool("gh__list_repos", map[string]any{}))
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
	if env["data"] == nil {
		t.Errorf("expected data key in envelope: %s", text)
	}
	data, _ := env["data"].(map[string]any)
	if _, hasID := data["id"]; !hasID {
		t.Errorf("expected id in data, got: %v", data)
	}
	if _, hasSecret := data["secret"]; hasSecret {
		t.Errorf("expected secret to be excluded from data, got: %v", data)
	}
}

func TestProxy_NestedExclusion_ReportsElidedPath(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("list_prs")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"items\":[{\"id\":1,\"body\":\"long body text here\"}]}"}]}`)
	addProxyConn(t, srv, "gh", conn)

	serveProxy(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "gh",
		"tool":       "list_prs",
		"projection": map[string]any{"exclude": []string{"body"}},
	}))

	resp := serveProxy(t, srv, callTool("gh__list_prs", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("proxy nested-exclude response: %s", text)

	env := parseProxyEnvelope(t, text)
	if !env.HasMini {
		t.Fatal("expected __mini envelope for nested exclusion")
	}
	if !strings.Contains(text, ".items[].body") {
		t.Errorf("expected excluded path .items[].body in __mini envelope, got: %s", text)
	}
	if env.File == "" {
		t.Errorf("expected __mini.file for nested exclusion (raw file must be written), got: %s", text)
	}
}

func TestProxy_IncludeFilter_PassthroughWhenAllFieldsIncluded(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("get_data")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"value\":\"data\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serveProxy(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_data",
		"projection": map[string]any{"include_only": []string{"id", "value"}},
	}))

	resp := serveProxy(t, srv, callTool("svc__get_data", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("include-filter no-exclusion response: %s", text)

	if strings.Contains(text, `"__mini"`) {
		t.Errorf("expected no __mini envelope when nothing excluded: %s", text)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Errorf("expected valid JSON response: %s", text)
	}
	if id, _ := parsed["id"].(float64); id != 1 {
		t.Errorf("expected id:1, got %v", parsed["id"])
	}
	if val, _ := parsed["value"].(string); val != "data" {
		t.Errorf("expected value:data, got %v", parsed["value"])
	}
}

func TestProxy_Call_WithTruncation_ProjectionNote(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("get_issue")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"body\":\"this is a very long body that will be truncated\"}"}]}`)
	addProxyConn(t, srv, "gh", conn)

	serveProxy(t, srv, callTool("config", map[string]any{
		"action": "set_projection",
		"server": "gh",
		"tool":   "get_issue",
		"projection": map[string]any{
			"string_limits": map[string]any{"body": 5},
		},
	}))

	resp := serveProxy(t, srv, callTool("gh__get_issue", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("truncation note response: %s", text)

	if !strings.Contains(text, "truncated") {
		t.Errorf("expected 'truncated' in projection note: %s", text)
	}
	if !strings.Contains(text, "body") {
		t.Errorf("expected field name 'body' in projection note: %s", text)
	}
}

func TestProxy_Call_MiniFormat_RendersLines(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serveProxy(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_user",
		"projection": map[string]any{"format": "mini"},
	}))

	resp := serveProxy(t, srv, callTool("svc__get_user", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("mini format response: %s", text)

	if !strings.Contains(text, "svc.get_user") {
		t.Errorf("mini format should contain server.tool header: %s", text)
	}
	if strings.HasPrefix(text, "{") {
		t.Errorf("mini format should not be raw JSON: %s", text)
	}
}

func TestProxy_Call_GlobalMiniFormat_Respected(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.ResponseFormat = "mini"
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	conn := fakeConn("get_user")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"name\":\"alice\"}"}]}`)
	addProxyConn(t, srv, "svc", conn)

	serveProxy(t, srv, callTool("config", map[string]any{
		"action":     "set_projection",
		"server":     "svc",
		"tool":       "get_user",
		"projection": map[string]any{"exclude": []string{}},
	}))

	resp := serveProxy(t, srv, callTool("svc__get_user", map[string]any{}))
	text := toolResultText(t, resp)
	t.Logf("global mini format: %s", text)

	if strings.HasPrefix(text, "{") {
		t.Errorf("global mini format should not be raw JSON: %s", text)
	}
}

func TestProxy_Call_MiniFormat_PerSession(t *testing.T) {
	// Exercises the daemon path: a session that received no _mini_tool_mode signal
	// is proxy by the zero-value default, with no server-level option set.
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer srv.Close()

	conn := fakeConn("list_items")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"[{\"id\":1,\"name\":\"foo\"},{\"id\":2,\"name\":\"bar\"}]"}]}`)
	addProxyConn(t, srv, "svc", conn)

	const sessionID = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
	postMCP(t, srv, sessionID, initMsg(false)) // no signal → proxy

	postMCP(t, srv, sessionID, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "config",
			"arguments": map[string]any{
				"action": "set_projection", "server": "svc", "tool": "list_items",
				"projection": map[string]any{"format": "mini"}, "session_only": true,
			},
		},
	})

	resp := postMCP(t, srv, sessionID, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "svc__list_items", "arguments": map[string]any{}},
	})

	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("no content in response")
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	t.Logf("per-session mini format output: %q", text)

	if !strings.Contains(text, "svc.list_items") {
		t.Errorf("expected [svc.list_items] header in mini format output: %s", text)
	}
	if strings.HasPrefix(text, "{") {
		t.Errorf("expected mini text format, got JSON: %s", text)
	}
}

func TestProxy_StandaloneServe_InheritsServerMode(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()

	msgs := serveAllProxy(t, srv, rpc("tools/list", nil))
	var tools []any
	for _, m := range msgs {
		if res, ok := m["result"].(map[string]any); ok {
			if t2, ok := res["tools"].([]any); ok {
				tools = t2
			}
		}
	}
	for _, tool := range tools {
		name := tool.(map[string]any)["name"].(string)
		if name == "perm_call" {
			t.Errorf("standalone proxy mode should not expose perm_call")
		}
	}
}

func TestProxy_MiniConfig_Status_Works(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("list_issues")
	addProxyConn(t, srv, "github", conn)

	resp := serveProxy(t, srv, callTool("config", map[string]any{"action": "status"}))
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
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("list_issues")
	addProxyConn(t, srv, "removeme", conn)

	msgs := serveAllProxy(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": "removeme",
	}))

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
	srv := newProxyServer(t)
	defer srv.Close()
	raw := json.RawMessage(`{"readOnlyHint":true}`)
	conn := fakeConnWithAnnotations("get_file", raw)
	addProxyConn(t, srv, "fs", conn)

	tools := toolsList(t, srv)
	tool := findTool(tools, "fs__get_file")
	if tool == nil {
		t.Fatal("fs__get_file not found in tools/list")
	}
	assertAnnotationsEqual(t, tool["annotations"], raw)
}

func TestProxy_ToolsList_MultiKeyAnnotationsPassthrough(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	raw := json.RawMessage(`{"readOnlyHint":true,"destructiveHint":false,"idempotentHint":true,"openWorldHint":false,"title":"Get File","fakeHint":true}`)
	conn := fakeConnWithAnnotations("get_file", raw)
	addProxyConn(t, srv, "fs2", conn)

	tools := toolsList(t, srv)
	tool := findTool(tools, "fs2__get_file")
	if tool == nil {
		t.Fatal("fs2__get_file not found in tools/list")
	}
	assertAnnotationsEqual(t, tool["annotations"], raw)
}

func TestProxy_ToolsList_AbsentAnnotationsOmitted(t *testing.T) {
	srv := newProxyServer(t)
	defer srv.Close()
	conn := fakeConn("write_file")
	addProxyConn(t, srv, "fs", conn)

	tools := toolsList(t, srv)
	tool := findTool(tools, "fs__write_file")
	if tool == nil {
		t.Fatal("fs__write_file not found in tools/list")
	}

	if _, hasAnnotations := tool["annotations"]; hasAnnotations {
		t.Errorf("tool without annotations must not include annotations key, got: %v", tool["annotations"])
	}
}
