//go:build test

package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestMain(m *testing.M) {
	if os.Getenv("MINI_HELPER_PROCESS") == "1" {
		runEnvEchoServer()
		os.Exit(0)
	}
	auth.UseLoopbackHTTPClient()
	auth.UseEphemeralCallbackPort()
	os.Exit(m.Run())
}

func runEnvEchoServer() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		id := req["id"]
		if id == nil {
			continue
		}
		switch method, _ := req["method"].(string); method {
		case "initialize":
			envEchoWrite(id, map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo":      map[string]any{"name": "envecho", "version": "0"},
				"capabilities":    map[string]any{},
			})
		case "tools/list":
			envEchoWrite(id, map[string]any{
				"tools": []any{map[string]any{
					"name":        "get_env",
					"description": "returns MINI_TEST_VAR",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
				}},
			})
		case "tools/call":
			envEchoWrite(id, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": os.Getenv("MINI_TEST_VAR")}},
			})
		default:
			envEchoError(id)
		}
	}
}

func envEchoWrite(id any, result any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": id, "result": result})
	fmt.Fprintf(os.Stdout, "%s\n", b)
}

func envEchoError(id any) {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": transport.JsonRPCVersion,
		"id":      id,
		"error": map[string]any{
			"code":    transport.CodeMethodNotFound,
			"message": "method not found",
		},
	})
	fmt.Fprintf(os.Stdout, "%s\n", b)
}

// Every server's response store keeps one cleanup timer pending on the server's clock.
const responseCleanupTimer = 1

func newTestServer(t *testing.T, p server.Params) *server.Server {
	t.Helper()
	if p.Config == nil {
		p.Config = config.DefaultConfig()
	}
	if p.Config.ResponseDir == "" {
		p.Config.ResponseDir = t.TempDir()
	}
	if p.ConfigDir == "" {
		p.ConfigDir = t.TempDir()
	}
	if p.Logger == nil {
		p.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	srv := server.New(p)
	t.Cleanup(srv.Close)
	return srv
}

func writeTestServerConfig(t *testing.T, srv *server.Server, sc config.ServerConfig) {
	t.Helper()
	configtest.WriteServer(t, srv.ConfigDir(), sc)
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
			"jsonrpc": transport.JsonRPCVersion, "id": id,
			"result": map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "0"},
			},
		})
	case "tools/list":
		json.NewEncoder(w).
			Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": id, "result": map[string]any{"tools": tools}})
	//nolint:errcheck
	default:
		json.NewEncoder(w).
			Encode(map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": id, "result": nil})
		//nolint:errcheck
	}
}

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
	input := rpc("initialize", initParams())
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
	req := map[string]any{"jsonrpc": transport.JsonRPCVersion, "id": 1, "method": method, "params": json.RawMessage(p)}
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

func initParams() map[string]any {
	params := map[string]any{
		"protocolVersion": transport.LegacyVersions[0],
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}
	return params
}

func serve(t *testing.T, srv *server.Server, input []byte) map[string]any {
	t.Helper()
	session := newTestSession(t, input)
	return session.response(t, session.run(srv))
}

type testSession struct {
	input  []byte
	wantID string
}

func newTestSession(t *testing.T, input []byte) testSession {
	t.Helper()
	var callReq struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(input), &callReq); err != nil || len(callReq.ID) == 0 {
		t.Fatalf("serve: could not extract id from input: %s", input)
	}
	initRaw, _ := json.Marshal(initParams())
	initReq, _ := json.Marshal(
		map[string]any{
			"jsonrpc": transport.JsonRPCVersion,
			"id":      0,
			"method":  "initialize",
			"params":  json.RawMessage(initRaw),
		},
	)
	return testSession{input: append(append(initReq, '\n'), input...), wantID: string(callReq.ID)}
}

// No *testing.T: inBackground calls run off the test goroutine, where t.Fatal isn't allowed.
func (ts testSession) run(srv *server.Server) []byte {
	var out bytes.Buffer
	srv.Serve(
		context.Background(),
		bytes.NewReader(ts.input),
		&out,
	) //nolint:errcheck // response reports a session that failed
	return out.Bytes()
}

func (ts testSession) response(t *testing.T, output []byte) map[string]any {
	t.Helper()
	return findResponseByID(t, output, ts.wantID)
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

type proxyEnvelope struct {
	HasMini bool
	Msg     string
	File    string
	Data    map[string]any
}

func parseProxyEnvelope(t *testing.T, text string) proxyEnvelope {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("expected JSON from proxy: %s", text)
	}
	env := proxyEnvelope{}
	if mini, ok := raw["__mini"].(map[string]any); ok {
		env.HasMini = true
		env.Msg, _ = mini["msg"].(string)
		env.File, _ = mini["file"].(string)
	}
	if data, ok := raw["data"].(map[string]any); ok {
		env.Data = data
	} else {
		env.Data = raw
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

func addTestConnection(params addConnectionParams) {
	params.T.Helper()
	params.Server.AddConnection(context.Background(), params.Config, params.Connection)
}

type requireRPCErrorParams struct {
	T          *testing.T
	Resp       map[string]any
	WantCode   int
	WantSubstr string
}

// requireRPCError asserts resp is a JSON-RPC error with the given code and message substring.
func requireRPCError(params requireRPCErrorParams) {
	params.T.Helper()
	errObj, ok := params.Resp["error"].(map[string]any)
	if !ok {
		params.T.Fatalf("expected JSON-RPC error with code %d, got result: %v", params.WantCode, params.Resp)
	}
	code, _ := errObj["code"].(float64)
	if int(code) != params.WantCode {
		params.T.Errorf("error code = %d, want %d (message: %v)", int(code), params.WantCode, errObj["message"])
	}
	if params.WantSubstr != "" {
		msg, _ := errObj["message"].(string)
		if !strings.Contains(msg, params.WantSubstr) {
			params.T.Errorf("error message %q does not contain %q", msg, params.WantSubstr)
		}
	}
}

// requireToolError asserts that resp is a successful JSON-RPC response containing a tool
// result with isError:true and content text matching wantSubstr.
func requireToolError(t *testing.T, resp map[string]any, wantSubstr string) {
	t.Helper()
	if resp["error"] != nil {
		t.Fatalf("expected tool error result, got RPC error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result in response: %v", resp)
	}
	if result["isError"] != true {
		t.Fatalf("expected isError=true, got result: %v", result)
	}
	if wantSubstr != "" {
		text := toolResultText(t, resp)
		if !strings.Contains(text, wantSubstr) {
			t.Errorf("tool error text %q does not contain %q", text, wantSubstr)
		}
	}
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

func TestCallOnUnknownServerIsInvalidParams(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	resp := serve(t, srv, callTool("nobody__doThing", map[string]any{}))
	requireRPCError(
		requireRPCErrorParams{T: t, Resp: resp, WantCode: transport.CodeInvalidParams, WantSubstr: "not found"},
	)
}

func TestConfigureStatus(t *testing.T) {
	srv := newTestServer(t, server.Params{})
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

func mustListTools(t *testing.T, srv *server.Server) []map[string]any {
	t.Helper()
	resp := serve(t, srv, rpc("tools/list", nil))
	result := resp["result"].(map[string]any)
	var tools []map[string]any
	for _, value := range result["tools"].([]any) {
		tools = append(tools, value.(map[string]any))
	}
	return tools
}

func resultJSONText(t *testing.T, resp map[string]any) string {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result: %v", resp)
	}
	if tools, ok := result["tools"]; ok {
		raw, err := json.Marshal(tools)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	return toolResultText(t, resp)
}
