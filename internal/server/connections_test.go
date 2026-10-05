//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func TestListDirectoryNonEmpty(t *testing.T) {
	fake := fakeConn("list_directory")
	fake.RespondWith(map[string]any{"entries": []string{"alpha.txt", "beta.go"}})
	srv := newTestServer(t, server.Params{})
	addTestConnection(t, srv, config.ServerConfig{Name: "fs"}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "list_directory", "params": map[string]any{"path": "/dir"},
	}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["error"] != nil {
		t.Fatalf("list_directory failed: %v", env)
	}
	if env["data"] == nil {
		t.Errorf("expected non-empty data, got: %v", env["data"])
	}
}

func TestReadFileTruncation(t *testing.T) {
	fake := fakeConn("read_file")
	fake.RespondWith(strings.Repeat("The quick brown fox jumps over the lazy dog. ", 50))

	cfg := config.DefaultConfig()
	cfg.DefaultStringLimit = 100
	srv := newTestServer(t, server.Params{Config: cfg})
	t.Cleanup(srv.Close)
	addTestConnection(t, srv, config.ServerConfig{Name: "fs"}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "fs", "tool": "read_file", "params": map[string]any{"path": "/big.txt"},
	}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["error"] != nil {
		t.Fatalf("read_file failed: %v", env)
	}
	truncated, _ := env["truncated"].([]any)
	if len(truncated) == 0 {
		t.Errorf("expected truncated entries in envelope after string limit applied, got: %v", env)
	}
}

func countToolsByPrefix(tools []map[string]any, prefix string) int {
	n := 0
	for _, tool := range tools {
		if name, _ := tool["name"].(string); strings.HasPrefix(name, prefix) {
			n++
		}
	}
	return n
}

func TestMultipleServers(t *testing.T) {
	fake1 := fakeConn("list_directory")
	fake1.RespondWith(map[string]any{"entries": []string{"from_server1.txt"}})
	fake2 := fakeConn("list_directory")
	fake2.RespondWith(map[string]any{"entries": []string{"from_server2.txt"}})
	srv := newTestServer(t, server.Params{})
	addTestConnection(t, srv, config.ServerConfig{Name: "fs1"}, fake1)
	addTestConnection(t, srv, config.ServerConfig{Name: "fs2"}, fake2)

	var tools []map[string]any
	json.Unmarshal([]byte(toolResultText(t, serve(t, srv, callTool("list", map[string]any{})))), &tools) //nolint:errcheck
	if countToolsByPrefix(tools, "fs1.") == 0 || countToolsByPrefix(tools, "fs2.") == 0 {
		t.Errorf("expected tools from both servers, got fs1=%d fs2=%d",
			countToolsByPrefix(tools, "fs1."), countToolsByPrefix(tools, "fs2."))
	}
	resp1 := serve(t, srv, callTool("call", map[string]any{
		"server": "fs1", "tool": "list_directory", "params": map[string]any{"path": "/dir"},
	}))
	if env1 := parseEnvelope(t, toolResultText(t, resp1)); env1["error"] != nil {
		t.Errorf("expected ok=true from fs1: %v", env1)
	}
}

func assertAddServer(t *testing.T, srv *server.Server) {
	t.Helper()
	resp := serve(t, srv, callTool("config", map[string]any{
		"action": "add_server",
		"config": map[string]any{"name": "dynamic_echo", "command": echomcpBin},
	}))
	text := toolResultText(t, resp)
	var result map[string]any
	json.Unmarshal([]byte(text), &result) //nolint:errcheck
	if result["error"] != nil {
		t.Fatalf("add_server failed: %s", text)
	}
	if text2 := toolResultText(t, serve(t, srv, callTool("list", map[string]any{}))); !strings.Contains(text2, "dynamic_echo") {
		t.Errorf("expected dynamic_echo tools after add_server: %s", text2)
	}
}

func assertRemoveServer(t *testing.T, srv *server.Server) {
	t.Helper()
	resp := serve(t, srv, callTool("config", map[string]any{"action": "remove_server", "server": "dynamic_echo"}))
	var result map[string]any
	json.Unmarshal([]byte(toolResultText(t, resp)), &result) //nolint:errcheck
	if result["error"] != nil {
		t.Fatalf("remove_server failed: %v", result)
	}
	if text := toolResultText(t, serve(t, srv, callTool("list", map[string]any{}))); strings.Contains(text, "dynamic_echo") {
		t.Errorf("dynamic_echo still present after remove: %s", text)
	}
}

func TestAddRemoveServer(t *testing.T) {
	if echomcpBin == "" {
		t.Fatal("ECHOMCP_BIN not set; run check.sh or: go build -o /tmp/echomcp ./cmd/echomcp && ECHOMCP_BIN=/tmp/echomcp go test ...")
	}
	cfg := config.DefaultConfig()
	cfg.DangerousAllowRuntimeStdio = true
	srv := newTestServer(t, server.Params{Config: cfg})
	t.Cleanup(srv.Close)
	assertAddServer(t, srv)
	assertRemoveServer(t, srv)
}

func TestStdioEnvPassthrough(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	t.Cleanup(srv.Close)

	sc := config.ServerConfig{
		Name:    "envtest",
		Command: os.Args[0],
		Env:     []string{"MINI_HELPER_PROCESS=1", "MINI_TEST_VAR=hello_from_mini"},
	}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("AddUpstream: %v", err)
	}
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "envtest", "tool": "get_env", "params": map[string]any{},
	}))
	if text := toolResultText(t, resp); !strings.Contains(text, "hello_from_mini") {
		t.Errorf("MINI_TEST_VAR not in subprocess response, got: %s", text)
	}
}

func TestAddUpstream_existingServer_replacesIt(t *testing.T) {
	serveTools := func(names ...string) string {
		var tools []map[string]any
		for _, n := range names {
			tools = append(tools, map[string]any{"name": n, "description": n, "inputSchema": map[string]any{"type": "object"}})
		}
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fakeMCPHandle(w, r, tools) }))
		t.Cleanup(ts.Close)
		return ts.URL
	}
	srv := newConnectTestServer(t)
	defer srv.Close()

	for _, url := range []string{serveTools("a"), serveTools("a", "b")} {
		if err := srv.AddUpstream(context.Background(), config.ServerConfig{Name: "svc", Transport: "http", URL: url}); err != nil {
			t.Fatalf("AddUpstream: %v", err)
		}
	}

	if got := srv.ToolCount("svc"); got != 2 {
		t.Errorf("expected the replacement's 2 tools, got %d", got)
	}
}

func TestAddUpstream_handshakeTimeoutSkipsHungStdioSubprocess(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	t.Cleanup(srv.Close)

	sc := config.ServerConfig{
		Name:             "hungupstream",
		Command:          "sleep",
		Args:             []string{"30"},
		HandshakeTimeout: "100ms",
	}
	start := time.Now()
	err := srv.AddUpstream(context.Background(), sc)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected AddUpstream to fail for an upstream that never answers initialize")
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("AddUpstream did not respect handshake_timeout, took %v", elapsed)
	}
}

func TestAddConnection_reAddWithStaleProjections_keepsLiveSetProjection(t *testing.T) {
	srv := newConfigServer(t)
	fake := fakeConn("getData")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2}"}]}`)
	writeTestServerConfig(t, srv, config.ServerConfig{Name: "svc"})
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, fake); err != nil {
		t.Fatal(err)
	}

	serve(t, srv, callTool("config", map[string]any{
		"action": "set_projection", "server": "svc", "tool": "getData",
		"projection": map[string]any{"include_only": []string{"a"}},
	}))

	newFake := fakeConn("getData")
	newFake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2}"}]}`)
	if err := srv.AddConnection(t.Context(), config.ServerConfig{
		Name: "svc",
		Projections: map[string]*config.ProjectionConfig{
			"getData": {IncludeOnly: []string{"b"}},
		},
	}, newFake); err != nil {
		t.Fatal(err)
	}

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "getData", "params": map[string]any{},
	}))
	data := parseProxyEnvelope(t, toolResultText(t, resp)).Data
	if data["a"] == nil {
		t.Errorf("live projection reverted by re-AddConnection: got %v", data)
	}
	if data["b"] != nil {
		t.Errorf("stale snapshot projection applied: b should be absent, got %v", data)
	}
}

func TestAddConnection_removeThenReAdd_usesNewProjections(t *testing.T) {
	srv := newConfigServer(t)
	fake := fakeConn("getData")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2}"}]}`)
	if err := srv.AddConnection(t.Context(), config.ServerConfig{
		Name: "svc",
		Projections: map[string]*config.ProjectionConfig{
			"getData": {IncludeOnly: []string{"a"}},
		},
	}, fake); err != nil {
		t.Fatal(err)
	}

	assertRemoveOk(t, srv, "svc")

	newFake := fakeConn("getData")
	newFake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"a\":1,\"b\":2}"}]}`)
	if err := srv.AddConnection(t.Context(), config.ServerConfig{
		Name: "svc",
		Projections: map[string]*config.ProjectionConfig{
			"getData": {IncludeOnly: []string{"b"}},
		},
	}, newFake); err != nil {
		t.Fatal(err)
	}

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "getData", "params": map[string]any{},
	}))
	data := parseProxyEnvelope(t, toolResultText(t, resp)).Data
	if data["b"] == nil {
		t.Errorf("expected new projection after remove+add, got %v", data)
	}
	if data["a"] != nil {
		t.Errorf("old projection still active after remove+add, got %v", data)
	}
}
