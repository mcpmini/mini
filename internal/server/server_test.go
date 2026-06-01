//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func newTestServer(t *testing.T) *server.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	return server.New(cfg, logger)
}

// newMCPTestServer starts a minimal HTTP MCP server advertising the given tools.
func newMCPTestServer(t *testing.T, tools []map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fakeMCPHandle(w, r, tools)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fakeMCPHandle(w http.ResponseWriter, r *http.Request, tools []map[string]any) {
	var req map[string]any
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	id := req["id"]
	switch req["method"] {
	case "initialize":
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"jsonrpc": "2.0", "id": id,
			"result": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "0"},
			},
		})
	case "tools/list":
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": tools}}) //nolint:errcheck
	default:
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": nil}) //nolint:errcheck
	}
}

// serveAll runs a full Serve session and returns all parsed output messages.
func serveAll(t *testing.T, srv *server.Server, lines ...[]byte) []map[string]any {
	t.Helper()
	input := buildServeInput(lines)
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Serve(ctx, bytes.NewReader(input), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return parseMessages(out.Bytes())
}

func buildServeInput(lines [][]byte) []byte {
	input := rpc("initialize", map[string]any{
		"protocolVersion": transport.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	for _, l := range lines {
		input = append(input, l...)
		if len(l) > 0 && l[len(l)-1] != '\n' {
			input = append(input, '\n')
		}
	}
	return input
}

func parseMessages(data []byte) []map[string]any {
	var msgs []map[string]any
	for _, raw := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			msgs = append(msgs, m)
		}
	}
	return msgs
}

// hasNotification returns true if any message has the given method and no id.
func hasNotification(msgs []map[string]any, method string) bool {
	for _, m := range msgs {
		if m["method"] == method && m["id"] == nil {
			return true
		}
	}
	return false
}

func fakeConn(tools ...string) *transport.FakeConnection {
	defs := make([]transport.ToolDefinition, len(tools))
	for i, name := range tools {
		defs[i] = transport.ToolDefinition{
			Name:        name,
			Description: "desc for " + name,
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}
	}
	return &transport.FakeConnection{Tools: defs, Responses: make(map[string]json.RawMessage)}
}

func rpc(method string, params any) []byte {
	p, _ := json.Marshal(params)
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": json.RawMessage(p)}
	b, _ := json.Marshal(req)
	return append(b, '\n')
}

func callTool(name string, args any) []byte {
	a, _ := json.Marshal(args)
	return rpc("tools/call", map[string]any{"name": name, "arguments": json.RawMessage(a)})
}

func parseResponse(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &resp); err != nil {
		t.Fatalf("parse response: %v\nraw: %s", err, data)
	}
	return resp
}

func serve(t *testing.T, srv *server.Server, input []byte) map[string]any {
	t.Helper()
	var callReq struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(input), &callReq); err != nil || len(callReq.ID) == 0 {
		t.Fatalf("serve: could not extract id from input: %s", input)
	}
	wantID := string(callReq.ID)

	initParams, _ := json.Marshal(map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	initReq, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize", "params": json.RawMessage(initParams)})
	fullInput := append(append(initReq, '\n'), input...)
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), bytes.NewReader(fullInput), &out) }()
	<-done
	return findResponseByID(t, out.Bytes(), wantID)
}

func findResponseByID(t *testing.T, data []byte, wantID string) map[string]any {
	t.Helper()
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var msg struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if string(msg.ID) == wantID {
			return parseResponse(t, line)
		}
	}
	t.Fatalf("no response with id=%s found in output: %s", wantID, data)
	return nil
}

func parseEnvelope(t *testing.T, text string) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("expected JSON envelope: %s", text)
	}
	return env
}

func toolResultText(t *testing.T, resp map[string]any) string {
	t.Helper()
	if errVal := resp["error"]; errVal != nil {
		t.Logf("rpc error: %v", errVal)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result in response: %v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content in result: %v", result)
	}
	return content[0].(map[string]any)["text"].(string)
}

func addTestConnection(t *testing.T, srv *server.Server, cfg config.ServerConfig, conn *transport.FakeConnection) {
	t.Helper()
	srv.AddConnection(context.Background(), cfg, conn)
}

// requireRPCError asserts resp is a JSON-RPC error with the given code and message substring.
func requireRPCError(t *testing.T, resp map[string]any, wantCode int, wantSubstr string) {
	t.Helper()
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected JSON-RPC error with code %d, got result: %v", wantCode, resp)
	}
	code, _ := errObj["code"].(float64)
	if int(code) != wantCode {
		t.Errorf("error code = %d, want %d (message: %v)", int(code), wantCode, errObj["message"])
	}
	if wantSubstr != "" {
		msg, _ := errObj["message"].(string)
		if !strings.Contains(msg, wantSubstr) {
			t.Errorf("error message %q does not contain %q", msg, wantSubstr)
		}
	}
}

func mustDiscoverResults(t *testing.T, srv *server.Server, args map[string]any) []map[string]any {
	t.Helper()
	text := toolResultText(t, serve(t, srv, callTool("list", args)))
	var results []map[string]any
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("discover result not JSON array: %v\n%s", err, text)
	}
	return results
}

func TestInitialize(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	in := bytes.NewReader(rpc("initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}))
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, in, &out) }()
	<-done

	resp := parseResponse(t, out.Bytes())
	result := resp["result"].(map[string]any)
	if result["protocolVersion"] != "2025-03-26" {
		t.Errorf("unexpected protocol version: %v", result["protocolVersion"])
	}
}

func TestDiscoverEmpty(t *testing.T) {
	srv := newTestServer(t)
	results := mustDiscoverResults(t, srv, map[string]any{})
	if len(results) != 0 {
		t.Errorf("expected empty discover, got %d tools", len(results))
	}
}

func TestDiscoverListsTools(t *testing.T) {
	srv := newTestServer(t)
	addTestConnection(t, srv, config.ServerConfig{Name: "ci"}, fakeConn("getBuild", "listPipelines"))
	results := mustDiscoverResults(t, srv, map[string]any{})
	if len(results) != 2 {
		t.Errorf("expected 2 tools, got %d", len(results))
	}
}

func TestDiscoverSearch(t *testing.T) {
	srv := newTestServer(t)
	addTestConnection(t, srv, config.ServerConfig{Name: "ci"}, fakeConn("getBuild", "listPipelines"))
	results := mustDiscoverResults(t, srv, map[string]any{"query": "build"})
	if len(results) != 1 || results[0]["name"] != "ci.getBuild" {
		t.Errorf("unexpected search results: %v", results)
	}
}

func TestExecuteRoutesToUpstream(t *testing.T) {
	srv := newTestServer(t)
	fake := fakeConn("getBuild")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"build ok"}]}`)
	addTestConnection(t, srv, config.ServerConfig{Name: "ci"}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "ci",
		"tool":   "getBuild",
		"params": map[string]any{"id": "123"},
	}))

	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
}

func TestExecuteRejectsProtectedTools(t *testing.T) {
	srv := newTestServer(t)
	perm := &config.PermissionsConfig{Protected: []string{"deleteProject"}}
	addTestConnection(t, srv, config.ServerConfig{Name: "ci", Permissions: perm}, fakeConn("deleteProject"))

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "ci",
		"tool":   "deleteProject",
		"params": map[string]any{},
	}))

	text := toolResultText(t, resp)
	if text == "" {
		t.Fatal("expected error message in content")
	}
	// Should mention perm_call
	if !bytes.Contains([]byte(text), []byte("perm_call")) {
		t.Errorf("error should mention perm_call, got: %s", text)
	}
}

func TestExecuteUnknownServer(t *testing.T) {
	// "errors in finding the tool … should be reported as an MCP error response"
	// Unknown server/tool → -32602 InvalidParams, not a soft tool error.
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L103
	srv := newTestServer(t)
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "nobody",
		"tool":   "doThing",
		"params": map[string]any{},
	}))
	requireRPCError(t, resp, transport.CodeInvalidParams, "not found")
}

func TestActionDispatchMergesDefaultArgs(t *testing.T) {
	srv := newTestServer(t)
	fake := fakeConn("list_pull_requests")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"[]"}]}`)
	addTestConnection(t, srv, config.ServerConfig{Name: "gh"}, fake)
	srv.RegisterAction(config.ActionConfig{
		Name:        "my_prs",
		Description: "My open PRs",
		Server:      "gh",
		Tool:        "list_pull_requests",
		DefaultArgs: map[string]any{"state": "open", "author": "me"},
	})

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "gh",
		"tool":   "my_prs",
		"params": map[string]any{"state": "closed"},
	}))
	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	assertUpstreamArgs(t, fake, map[string]any{"state": "closed", "author": "me"})
}

func assertUpstreamArgs(t *testing.T, fake *transport.FakeConnection, want map[string]any) {
	t.Helper()
	var callParams transport.ToolCallParams
	if err := json.Unmarshal(fake.LastParams, &callParams); err != nil {
		t.Fatalf("unmarshal upstream params: %v", err)
	}
	for k, wantVal := range want {
		if got := callParams.Arguments[k]; got != wantVal {
			t.Errorf("expected %s=%v, got %v", k, wantVal, got)
		}
	}
}

func TestConfigureStatus(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	srv.AddConnection(ctx, config.ServerConfig{Name: "ci"}, fakeConn("getBuild", "listBuilds"))

	resp := serve(t, srv, callTool("config", map[string]any{"action": "status"}))
	text := toolResultText(t, resp)

	var status map[string]any
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("status not JSON: %v\n%s", err, text)
	}
	if status["servers"] == nil {
		t.Error("expected servers in status")
	}
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return real
}

// TestPing_ReturnsEmptyResult verifies the ping response is exactly {}.
// "The receiver MUST respond promptly with an empty response."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/utilities/ping.mdx#L24
func TestPing_ReturnsEmptyResult(t *testing.T) {
	srv := newTestServer(t)
	resp := serve(t, srv, rpc("ping", nil))
	if resp["error"] != nil {
		t.Fatalf("ping returned error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || len(result) != 0 {
		t.Errorf("ping result must be {}, got: %v", resp["result"])
	}
}

// TestInitialize_CapabilitiesListChanged verifies that the server declares
// tools.listChanged:true — required when the server emits tools/list_changed notifications.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/server/tools.mdx#L28
func TestInitialize_CapabilitiesListChanged(t *testing.T) {
	srv := newTestServer(t)
	msgs := serveAll(t, srv)
	for _, m := range msgs {
		if m["id"] != float64(1) {
			continue
		}
		result, _ := m["result"].(map[string]any)
		caps, _ := result["capabilities"].(map[string]any)
		tools, _ := caps["tools"].(map[string]any)
		if lc, _ := tools["listChanged"].(bool); !lc {
			t.Errorf("capabilities.tools.listChanged must be true, got: %v", tools)
		}
		return
	}
	t.Fatal("no initialize response found")
}

// TestInitialize_versionNegotiation verifies that the server always responds with its own
// supported version regardless of what the client requests. Clients that cannot handle
// the server's version SHOULD disconnect (the client's responsibility, not the server's).
// Spec: "If the server supports the requested protocol version, it MUST respond with the same
// version. Otherwise, the server MUST respond with another protocol version it supports."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L128
func TestInitialize_versionNegotiation(t *testing.T) {
	for _, clientVer := range []string{"2024-11-05", "2025-03-26", "99.99.99", ""} {
		t.Run("client="+clientVer, func(t *testing.T) {
			srv := newTestServer(t)
			resp := serve(t, srv, rpc("initialize", map[string]any{
				"protocolVersion": clientVer,
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "test", "version": "0"},
			}))
			if resp["error"] != nil {
				t.Fatalf("initialize error for client version %q: %v", clientVer, resp["error"])
			}
			result, _ := resp["result"].(map[string]any)
			got, _ := result["protocolVersion"].(string)
			if got != transport.ProtocolVersion {
				t.Errorf("server protocolVersion = %q, want %q", got, transport.ProtocolVersion)
			}
		})
	}
}

// TestInitialize_serverInfoPresent verifies the initialize response contains serverInfo
// with non-empty name and version fields.
// Spec: "The server MUST respond with its own capabilities and information."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L79
func TestInitialize_serverInfoPresent(t *testing.T) {
	srv := newTestServer(t)
	resp := serve(t, srv, rpc("initialize", map[string]any{
		"protocolVersion": transport.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}))
	result, _ := resp["result"].(map[string]any)
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] == "" || info["name"] == nil {
		t.Errorf("serverInfo.name must be non-empty, got: %v", info)
	}
	if info["version"] == "" || info["version"] == nil {
		t.Errorf("serverInfo.version must be non-empty, got: %v", info)
	}
}

// TestInitialize_doubleInitialize verifies that sending initialize twice is handled
// gracefully (idempotent). The spec does not prohibit re-initialization.
func TestInitialize_doubleInitialize(t *testing.T) {
	srv := newTestServer(t)
	secondInit := rpc("initialize", map[string]any{
		"protocolVersion": transport.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	// Use a distinct id for the second initialize so we can find it.
	var second map[string]any
	json.Unmarshal(bytes.TrimSpace(secondInit), &second) //nolint:errcheck
	second["id"] = 99
	secondInit, _ = json.Marshal(second)
	secondInit = append(secondInit, '\n')

	msgs := serveAll(t, srv, secondInit)
	for _, m := range msgs {
		if m["id"] == float64(99) {
			if m["error"] != nil {
				t.Errorf("second initialize returned error: %v", m["error"])
			}
			result, _ := m["result"].(map[string]any)
			if result["protocolVersion"] == nil {
				t.Errorf("second initialize response missing protocolVersion: %v", m)
			}
			return
		}
	}
	t.Error("no response for second initialize found")
}

func TestUnknownTool_Returns32602(t *testing.T) {
	srv := newTestServer(t)
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
	srv := newTestServer(t)
	fake := fakeConn("ping")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"pong"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "ping",
	}))
	if resp["error"] != nil {
		t.Fatalf("expected tool result, not RPC error: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Error("expected content in result")
	}
}
