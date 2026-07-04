//go:build test

package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func newCodeModeServer(t *testing.T, enabled bool) *server.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.CodeMode.Enabled = enabled
	srv := server.New(cfg, logger)
	t.Cleanup(srv.Close)
	return srv
}

func compactToolNames(t *testing.T, srv *server.Server) []string {
	t.Helper()
	resp := serve(t, srv, rpc("tools/list", nil))
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.(map[string]any)["name"].(string)
	}
	return names
}

func TestExecuteCode_Disabled(t *testing.T) {
	srv := newCodeModeServer(t, false)

	t.Run("omitted from tools/list", func(t *testing.T) {
		names := compactToolNames(t, srv)
		for _, name := range names {
			if name == "execute_code" {
				t.Fatalf("expected execute_code to be absent, got tools: %v", names)
			}
		}
	})

	t.Run("call rejected as unknown tool", func(t *testing.T) {
		resp := serve(t, srv, callTool("execute_code", map[string]any{"code": "async (input) => input"}))
		errObj, _ := resp["error"].(map[string]any)
		if errObj == nil {
			t.Fatalf("expected JSON-RPC error, got: %v", resp)
		}
		if msg, _ := errObj["message"].(string); !strings.Contains(msg, "unknown tool") {
			t.Errorf("expected message to mention unknown tool, got: %v", msg)
		}
	})
}

func TestExecuteCode_EnabledToolsList(t *testing.T) {
	srv := newCodeModeServer(t, true)

	t.Run("compact mode", func(t *testing.T) {
		names := compactToolNames(t, srv)
		if !containsString(names, "execute_code") {
			t.Errorf("expected execute_code in compact tools/list, got: %v", names)
		}
	})

	t.Run("proxy mode", func(t *testing.T) {
		tools := toolsList(t, srv)
		if !containsName(tools, "execute_code") {
			t.Errorf("expected execute_code in proxy tools/list, got: %v", toolNames(tools))
		}
	})
}

func TestExecuteCode_InputSchemaDeclaresExplicitTypeUnion(t *testing.T) {
	srv := newCodeModeServer(t, true)
	resp := serve(t, srv, rpc("tools/list", nil))
	input := executeCodeInputProperty(t, resp)
	types, _ := input["type"].([]any)
	if len(types) == 0 {
		t.Fatalf("input.type must be an explicit type union — untyped properties get string-encoded by some MCP clients — got: %v", input["type"])
	}
}

func executeCodeInputProperty(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	for _, tool := range tools {
		m, _ := tool.(map[string]any)
		if m["name"] != "execute_code" {
			continue
		}
		schema, _ := m["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		input, _ := props["input"].(map[string]any)
		if input == nil {
			t.Fatal("execute_code inputSchema has no input property")
		}
		return input
	}
	t.Fatal("execute_code not found in tools/list")
	return nil
}

func TestExecuteCode_MissingCode(t *testing.T) {
	srv := newCodeModeServer(t, true)
	resp := serve(t, srv, callTool("execute_code", map[string]any{}))
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected JSON-RPC error, got: %v", resp)
	}
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, "requires code") {
		t.Errorf("expected message to mention required code, got: %v", msg)
	}
}

func TestExecuteCode_WithDeno(t *testing.T) {
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}
	srv := newCodeModeServer(t, true)

	t.Run("trivial call returns computed result", func(t *testing.T) {
		resp := serve(t, srv, callTool("execute_code", map[string]any{
			"code":  "async (input) => input.n + 1",
			"input": map[string]any{"n": 41},
		}))
		text := toolResultText(t, resp)
		if text != "42" {
			t.Errorf("expected result text %q, got %q", "42", text)
		}
	})

	t.Run("syntax error surfaces as isError", func(t *testing.T) {
		resp := serve(t, srv, callTool("execute_code", map[string]any{
			"code": "async (input) =>",
		}))
		result, _ := resp["result"].(map[string]any)
		if result == nil || result["isError"] != true {
			t.Fatalf("expected isError=true, got: %v", resp)
		}
		text := toolResultText(t, resp)
		if !strings.Contains(text, "syntax") {
			t.Errorf("expected error text to mention syntax, got: %v", text)
		}
	})
}

func newCodeModeServerWithGrants(t *testing.T, urlAllowList []string) *server.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.CodeMode.Enabled = true
	cfg.CodeMode.URLAllowList = urlAllowList
	srv := server.New(cfg, logger)
	t.Cleanup(srv.Close)
	return srv
}

func TestExecuteCode_NetGrantFromConfigAllowsListedHost(t *testing.T) {
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("granted")) //nolint:errcheck // test server, best-effort write
	}))
	defer ts.Close()

	srv := newCodeModeServerWithGrants(t, []string{hostPort(t, ts)})

	resp := serve(t, srv, callTool("execute_code", map[string]any{
		"code":  `async (input) => { const r = await fetch(input.url); return await r.text(); }`,
		"input": map[string]any{"url": ts.URL},
	}))
	if text := toolResultText(t, resp); text != `"granted"` {
		t.Errorf("expected fetched body %q, got %q", `"granted"`, text)
	}
}

func TestExecuteCode_NoNetGrantDeniesFetch(t *testing.T) {
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer ts.Close()

	srv := newCodeModeServer(t, true)
	resp := serve(t, srv, callTool("execute_code", map[string]any{
		"code":  `async (input) => { await fetch(input.url); }`,
		"input": map[string]any{"url": ts.URL},
	}))
	result, _ := resp["result"].(map[string]any)
	if result == nil || result["isError"] != true {
		t.Fatalf("expected isError=true, got: %v", resp)
	}
	if text := toolResultText(t, resp); !strings.Contains(text, "net access") {
		t.Errorf("expected net access denial, got: %v", text)
	}
}

func hostPort(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split host:port from %q: %v", u.Host, err)
	}
	return "127.0.0.1:" + port
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func TestExecuteCode_ToolBridge(t *testing.T) {
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}

	srv := newCodeModeServer(t, true)
	fake := fakeConn("toolA", "toolB")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	fake.Tools[1].InputSchema = json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"}},"required":["owner","repo"]}`)
	addTestConnection(t, srv, config.ServerConfig{Name: "myserver"}, fake)

	t.Run("mini.call returns upstream content", func(t *testing.T) {
		resp := serve(t, srv, callTool("execute_code", map[string]any{
			"code": `async () => await mini.call("myserver", "toolA", {})`,
		}))
		text := toolResultText(t, resp)
		if text != `"ok"` {
			t.Errorf("expected %q, got %q", `"ok"`, text)
		}
	})

	t.Run("unknown tool returns isError", func(t *testing.T) {
		resp := serve(t, srv, callTool("execute_code", map[string]any{
			"code": `async () => await mini.call("myserver", "unknownTool", {})`,
		}))
		result, _ := resp["result"].(map[string]any)
		if result == nil || result["isError"] != true {
			t.Fatalf("expected isError=true, got: %v", resp)
		}
		if text := toolResultText(t, resp); !strings.Contains(text, "tool not found") {
			t.Errorf("expected error to mention tool not found, got: %q", text)
		}
	})

	t.Run("missing required param explains expected params", func(t *testing.T) {
		resp := serve(t, srv, callTool("execute_code", map[string]any{
			"code": `async () => await mini.call("myserver", "toolB", { owner: "a" })`,
		}))
		result, _ := resp["result"].(map[string]any)
		if result == nil || result["isError"] != true {
			t.Fatalf("expected isError=true, got: %v", resp)
		}
		text := toolResultText(t, resp)
		if !strings.Contains(text, `missing required "repo"`) {
			t.Errorf("expected missing required repo, got: %q", text)
		}
		if !strings.Contains(text, "owner (string, required)") {
			t.Errorf("expected expected-params summary with owner, got: %q", text)
		}
	})

	t.Run("mini.list exposes input schema", func(t *testing.T) {
		resp := serve(t, srv, callTool("execute_code", map[string]any{
			"code": `async () => { const t = (await mini.list()).find((x) => x.name === "myserver.toolA"); return t.inputSchema.type; }`,
		}))
		text := toolResultText(t, resp)
		if text != `"object"` {
			t.Errorf("expected inputSchema.type=%q, got %q", `"object"`, text)
		}
	})
}

func TestExecuteCode_ToolBridge_ProtectedTool(t *testing.T) {
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}

	srv := newCodeModeServer(t, true)
	fake := fakeConn("secret")
	addTestConnection(t, srv, config.ServerConfig{
		Name:        "myserver",
		Permissions: &config.PermissionsConfig{Protected: []string{"secret"}},
	}, fake)

	resp := serve(t, srv, callTool("execute_code", map[string]any{
		"code": `async () => await mini.call("myserver", "secret", {})`,
	}))
	result, _ := resp["result"].(map[string]any)
	if result == nil || result["isError"] != true {
		t.Fatalf("expected isError=true, got: %v", resp)
	}
	text := toolResultText(t, resp)
	if !strings.Contains(text, "protected") && !strings.Contains(text, "perm_call") {
		t.Errorf("expected error to mention protected or perm_call, got: %q", text)
	}
}
