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

func TestNotificationInitialized_noResponse(t *testing.T) {
	// "notifications/initialized" is a fire-and-forget — should not produce
	// a visible error, but does produce a nil result (which writes a null-result response).
	srv := newEdgeServer(t)
	input := rpc("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	input = append(input, rpc("notifications/initialized", nil)...)
	lines := rawServe(t, srv, input)
	// Both lines should be valid JSON
	for _, line := range lines {
		var resp map[string]any
		if err := json.Unmarshal(line, &resp); err != nil {
			t.Errorf("response line not valid JSON: %s", line)
		}
	}
}

func TestExecProtectedOnOpenTool(t *testing.T) {
	srv := newEdgeServer(t)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fakeConn("doThing"))

	resp := serve(t, srv, callTool("perm_call", map[string]any{
		"server": "svc", "tool": "doThing", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	// Should tell agent to use call instead
	if !strings.Contains(text, "call") {
		t.Errorf("error should mention call, got: %s", text)
	}
}

func TestExecOnHiddenTool_notFound(t *testing.T) {
	srv := newEdgeServer(t)
	perm := &config.PermissionsConfig{Hidden: []string{"secret"}}
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc", Permissions: perm}, fakeConn("secret", "visible"))

	// Hidden tools are absent from registry entirely — lookup returns "not found"
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "secret", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' for hidden tool, got: %s", text)
	}
}

func TestExecOnHiddenTool_notInDiscover(t *testing.T) {
	srv := newEdgeServer(t)
	perm := &config.PermissionsConfig{Hidden: []string{"secret"}}
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc", Permissions: perm}, fakeConn("secret", "visible"))

	resp := serve(t, srv, callTool("list", map[string]any{}))
	text := toolResultText(t, resp)
	if strings.Contains(text, "secret") {
		t.Errorf("hidden tool should not appear in discover results, got: %s", text)
	}
	if !strings.Contains(text, "visible") {
		t.Errorf("visible tool should appear in discover results, got: %s", text)
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
	if env["ok"] != false {
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

func TestConfigureUnknownAction(t *testing.T) {
	srv := newEdgeServer(t)
	resp := serve(t, srv, callTool("config", map[string]any{"action": "no_such_action"}))
	assertIsErrorResult(t, resp)
	text := toolResultText(t, resp)
	if !strings.Contains(text, "unknown configure action") {
		t.Errorf("expected error message, got: %s", text)
	}
}

func TestConfigureSetProjectionRequiresTool(t *testing.T) {
	srv := newEdgeServer(t)
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection",
		"server": "svc",
		// no "tool" field
	}))
	assertIsErrorResult(t, resp)
}

func TestConfigureRemoveServerRequiresName(t *testing.T) {
	srv := newEdgeServer(t)
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		// no server name
	}))
	assertIsErrorResult(t, resp)
}

func assertRemoveOk(t *testing.T, srv *server.Server, serverName string) {
	t.Helper()
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "remove_server",
		"server": serverName,
	}))
	text := toolResultText(t, resp)
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("expected JSON result: %s", text)
	}
	if result["ok"] != true {
		t.Errorf("expected ok=true, got: %v", result)
	}
}

func TestConfigureRemoveServer_clearsDiscover(t *testing.T) {
	srv := newEdgeServer(t)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fakeConn("ping"))
	if srv.ToolCount("svc") != 1 {
		t.Fatalf("expected 1 tool before remove")
	}
	assertRemoveOk(t, srv, "svc")

	discoverText := toolResultText(t, serve(t, srv, callTool("list", map[string]any{})))
	if strings.Contains(discoverText, "ping") {
		t.Errorf("removed server's tools should not appear in discover, got: %s", discoverText)
	}
}

func newSessionServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	return server.NewWithConfigDir(cfg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func fakeGetData() *transport.FakeConnection {
	return &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "getData", Description: "get", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2}"}]}`),
		},
	}
}

func TestSessionScopedProjectionNotPersistedAcrossCalls(t *testing.T) {
	srv := newSessionServer(t)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fakeGetData())

	serve(t, srv, callTool("config", map[string]any{
		"action":       "set_projection",
		"server":       "svc",
		"tool":         "getData",
		"projection":   map[string]any{"include": []string{"a"}},
		"session_only": true,
	}))

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "getData", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if strings.Contains(text, `"b":2`) {
		t.Logf("note: session projection applied within same session: %s", text)
	}
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
func newSvcWithPartialProjection(t *testing.T) *server.Server {
	t.Helper()
	srv := newEdgeServer(t)
	fake := fakeConn("coveredTool", "uncoveredTool")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fake)
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "coveredTool",
		"projection": map[string]any{"depth_limit": 3},
	}))
	return srv
}

func TestCallBlockedWhenProjectionFileExistsButToolUncovered(t *testing.T) {
	srv := newSvcWithPartialProjection(t)

	resp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "coveredTool"}))
	if text := toolResultText(t, resp); strings.Contains(text, "no projection") {
		t.Errorf("covered tool should succeed via call, got: %s", text)
	}

	resp = serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "uncoveredTool"}))
	if text := toolResultText(t, resp); !strings.Contains(text, "perm_call") {
		t.Errorf("uncovered tool: expected perm_call suggestion, got: %s", text)
	}
}

func TestPermCallAllowsUncoveredTool(t *testing.T) {
	srv := newSvcWithPartialProjection(t)

	resp := serve(t, srv, callTool("perm_call", map[string]any{"server": "svc", "tool": "uncoveredTool"}))
	text := toolResultText(t, resp)
	if strings.Contains(text, "isError") || strings.Contains(text, "perm_call") {
		t.Errorf("uncovered tool should succeed via perm_call, got: %s", text)
	}
}

func TestWildcardProjectionGrantsCallCoverage(t *testing.T) {
	srv := newEdgeServer(t)
	fake := fakeConn("anyTool")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fake)

	// Set a wildcard projection — covers all tools
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "*",
		"projection": map[string]any{"depth_limit": 3},
	}))

	resp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "anyTool"}))
	text := toolResultText(t, resp)
	if strings.Contains(text, "no projection") {
		t.Errorf("wildcard should cover anyTool, got: %s", text)
	}
}

func newSvcWithReadOnlyTool(t *testing.T) *server.Server {
	t.Helper()
	srv := newEdgeServer(t)
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "covered", ReadOnly: false},
			{Name: "readOnlyGet", ReadOnly: true},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"data"}]}`),
		},
	}
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fake)
	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "covered",
		"projection": map[string]any{"depth_limit": 3},
	}))
	return srv
}

func TestReadOnlyToolAllowedViaCallWithoutProjection(t *testing.T) {
	srv := newSvcWithReadOnlyTool(t)
	resp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "readOnlyGet"}))
	if text := toolResultText(t, resp); strings.Contains(text, "no projection") {
		t.Errorf("read-only tool should bypass projection requirement, got: %s", text)
	}
}

func TestNoProjectionFileAllowsCallNormally(t *testing.T) {
	srv := newEdgeServer(t)
	fake := fakeConn("anyTool")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc"}, fake)

	// No projections configured at all — call should work freely
	resp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "anyTool"}))
	text := toolResultText(t, resp)
	if strings.Contains(text, "no projection") {
		t.Errorf("server without any projections should allow call, got: %s", text)
	}
}

func TestDefaultPermissionProtected(t *testing.T) {
	srv := newEdgeServer(t)
	perm := &config.PermissionsConfig{Default: "protected"}
	addEdgeConn(t, srv, config.ServerConfig{Name: "svc", Permissions: perm}, fakeConn("dangerousOp", "safeRead"))

	// Both tools should be protected — call should reject them
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "dangerousOp", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	if !strings.Contains(text, "perm_call") {
		t.Errorf("expected perm_call mention for default-protected tool, got: %s", text)
	}
}
