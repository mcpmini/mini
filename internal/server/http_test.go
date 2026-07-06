//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func mcpPost(t *testing.T, ts *httptest.Server, body []byte, sessionID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func initRequest() []byte {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	})
	return b
}

func TestHTTPServer_initializeCreatesSession(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	resp := mcpPost(t, ts, initRequest(), "")
	defer resp.Body.Close()
	mustStatus(t, resp, http.StatusOK)
	if resp.Header.Get("Mcp-Session-Id") == "" {
		t.Error("expected Mcp-Session-Id header to be set")
	}
}

func initSession(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	resp := mcpPost(t, ts, initRequest(), "")
	sessionID := resp.Header.Get("Mcp-Session-Id")
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return sessionID
}

func initCompactRequest() []byte {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion":       "2024-11-05",
			"capabilities":          map[string]any{},
			"clientInfo":            map[string]any{"name": "test", "version": "0"},
			transport.ToolModeParam: transport.ToolModeCompactValue,
		},
	})
	return b
}

func initCompactSession(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	resp := mcpPost(t, ts, initCompactRequest(), "")
	sessionID := resp.Header.Get("Mcp-Session-Id")
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return sessionID
}

func drainMCPPost(t *testing.T, ts *httptest.Server, body []byte, sessionID string) {
	t.Helper()
	resp := mcpPost(t, ts, body, sessionID)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

type sessionProjectionParams struct {
	TS        *httptest.Server
	SessionID string
	SrvName   string
	Tool      string
	Proj      map[string]any
}

func setSessionProjection(t *testing.T, p sessionProjectionParams) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "config", "arguments": map[string]any{
			"action": "set_projection", "server": p.SrvName, "tool": p.Tool,
			"projection": p.Proj, "session_only": true,
		}},
	})
	drainMCPPost(t, p.TS, body, p.SessionID)
}

func httpExecToolText(t *testing.T, ts *httptest.Server, sessionID, srvName, tool string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"server": srvName, "tool": tool}},
	})
	resp := mcpPost(t, ts, body, sessionID)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var rpc struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
		} `json:"result"`
	}
	json.Unmarshal(b, &rpc) //nolint:errcheck
	if len(rpc.Result.Content) == 0 {
		return ""
	}
	return rpc.Result.Content[0].Text
}

func newHTTPTestServer(t *testing.T, opts ...server.ServerOption) (*server.Server, *httptest.Server) {
	t.Helper()
	srv := newTestServer(t, opts...)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return srv, ts
}

func mustStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("expected %d, got %d", want, resp.StatusCode)
	}
}

func requestMCPMethod(t *testing.T, ts *httptest.Server, method string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func requestToolsList(t *testing.T, ts *httptest.Server, accept string) *http.Response {
	t.Helper()
	sessionID := initSession(t, ts)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func assertAllowMethods(t *testing.T, resp *http.Response) {
	t.Helper()
	if allow := resp.Header.Get("Allow"); allow != "GET, POST" {
		t.Errorf("expected Allow: GET, POST, got %q", allow)
	}
}

func TestHTTPServer_sessionPersistsProjection(t *testing.T) {
	srv, ts := newHTTPTestServer(t)
	fake := fakeConn("get_item")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"x\"}"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake) //nolint:errcheck
	sessionID := initCompactSession(t, ts)
	setSessionProjection(t, sessionProjectionParams{TS: ts, SessionID: sessionID, SrvName: "svc", Tool: "get_item", Proj: map[string]any{"include_only": []string{"id"}}})
	text := httpExecToolText(t, ts, sessionID, "svc", "get_item")
	if text == "" {
		t.Fatal("no tool result — the projection assertion below would pass vacuously")
	}
	var env map[string]any
	json.Unmarshal([]byte(text), &env) //nolint:errcheck
	data, _ := json.Marshal(env["data"])
	if bytes.Contains(data, []byte("secret")) {
		t.Errorf("session projection should have excluded 'secret' from data, got: %s", data)
	}
}

func TestHTTPServer_deleteReturns405(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	resp := requestMCPMethod(t, ts, http.MethodDelete)
	defer resp.Body.Close()
	mustStatus(t, resp, http.StatusMethodNotAllowed)
	assertAllowMethods(t, resp)
}

func TestHTTPServer_methodNotAllowed(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	resp := requestMCPMethod(t, ts, http.MethodGet)
	defer resp.Body.Close()
	mustStatus(t, resp, http.StatusNotAcceptable)
}

func TestHTTPServer_healthz(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	resp, err := ts.Client().Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	mustStatus(t, resp, http.StatusOK)
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body) //nolint:errcheck
	if body["ok"] != true {
		t.Errorf("expected ok=true, got: %v", body)
	}
}

func httpExecOne(t *testing.T, ts *httptest.Server, id int, errs chan<- string) {
	t.Helper()
	// Each goroutine gets its own session, which requires initialize first.
	// Spec: "The initialization phase MUST be the first interaction between client and server."
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L38
	sessionID := initCompactSession(t, ts)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id + 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"server": "svc", "tool": "ping"}},
	})
	resp := mcpPost(t, ts, body, sessionID)
	if resp.StatusCode != http.StatusOK {
		errs <- "unexpected status"
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(b, []byte("pong")) {
		errs <- "missing tool result: " + string(b)
	}
}

func assertConcurrentExecs(t *testing.T, ts *httptest.Server, n int) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := range n {
		wg.Add(1)
		go func(i int) { defer wg.Done(); httpExecOne(t, ts, i, errs) }(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestHTTPServer_concurrentRequests(t *testing.T) {
	srv, ts := newHTTPTestServer(t)
	fake := &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Responses: map[string]json.RawMessage{"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"pong"}]}`)},
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake) //nolint:errcheck
	assertConcurrentExecs(t, ts, 10)
}

func TestHTTPServer_notFound(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	resp, err := ts.Client().Get(ts.URL + "/unknown")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func sseProxyToolCall(t *testing.T, ts *httptest.Server, sessionID, toolName string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": toolName, "arguments": map[string]any{}},
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHTTPServer_SSEResponse(t *testing.T) {
	srv, ts := newHTTPTestServer(t)
	fake := fakeConn("myTool")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake) //nolint:errcheck
	sessionID := initSession(t, ts)
	resp := sseProxyToolCall(t, ts, sessionID, "svc__myTool")
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type: text/event-stream, got: %q", ct)
	}
	data, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(data, []byte("event: message\n")) {
		t.Errorf("expected SSE event: message, got:\n%s", data)
	}
	if !bytes.Contains(data, []byte("data: {")) {
		t.Errorf("expected SSE data line with JSON, got:\n%s", data)
	}
}

func TestHTTPServer_JSONResponseWhenNoSSEAccept(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	resp := requestToolsList(t, ts, "application/json")
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type: application/json, got: %q", ct)
	}
}

func TestHTTPServer_SSEWithBothAcceptTypes(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	resp := requestToolsList(t, ts, "application/json, text/event-stream")
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected SSE when Accept includes text/event-stream, got: %q", ct)
	}
}

func TestHTTPServer_GetAllowHeader(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	for _, method := range []string{http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			resp := requestMCPMethod(t, ts, method)
			resp.Body.Close()
			mustStatus(t, resp, http.StatusMethodNotAllowed)
			assertAllowMethods(t, resp)
		})
	}
}

func TestHTTPServer_SSEXAccelBuffering(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	resp := requestToolsList(t, ts, "text/event-stream")
	defer resp.Body.Close()
	if v := resp.Header.Get("X-Accel-Buffering"); v != "no" {
		t.Errorf("expected X-Accel-Buffering: no, got %q", v)
	}
}

// TestHTTPServer_staleSessionFails proves that a session ID from a dead daemon instance
// gets a prompt error rather than blocking indefinitely on the new daemon.
func TestHTTPServer_staleSessionFails(t *testing.T) {
	_, ts1 := newHTTPTestServer(t)
	sessionID := initSession(t, ts1)
	ts1.Close() // simulate daemon restart

	_, ts2 := newHTTPTestServer(t)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "list", "arguments": map[string]any{}},
	})

	done := make(chan struct{})
	go func() {
		resp := mcpPost(t, ts2, body, sessionID)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var rpc map[string]any
		json.Unmarshal(b, &rpc) //nolint:errcheck
		if rpc["error"] == nil {
			t.Errorf("expected JSON-RPC error for stale session, got: %s", b)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stale session blocked indefinitely — daemon restart hang not fixed")
	}
}

func TestHTTPServer_CrossOriginRejected(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(initRequest()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example.com")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	mustStatus(t, resp, http.StatusForbidden)
}

func TestDaemonAuth_HealthzUnauthenticated(t *testing.T) {
	ts := newAuthHTTPTestServer(t)
	resp, err := ts.Client().Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	mustStatus(t, resp, http.StatusOK)
}

func TestHTTPServer_AllowNonLoopbackHostSkipsHostCheck(t *testing.T) {
	_, ts := newHTTPTestServer(t, server.WithAllowNonLoopbackHost())
	resp := postWithHost(t, ts.URL, "203.0.113.5:4857")
	resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("non-loopback Host should be allowed when configured, got 403")
	}
}
