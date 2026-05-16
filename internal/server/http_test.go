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

func drainMCPPost(t *testing.T, ts *httptest.Server, body []byte, sessionID string) {
	t.Helper()
	resp := mcpPost(t, ts, body, sessionID)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func setSessionProjection(t *testing.T, ts *httptest.Server, sessionID, srvName, tool string, proj map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "config", "arguments": map[string]any{
			"action": "set_projection", "server": srvName, "tool": tool,
			"projection": proj, "session_only": true,
		}},
	})
	drainMCPPost(t, ts, body, sessionID)
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

func newHTTPTestServer(t *testing.T) (*server.Server, *httptest.Server) {
	t.Helper()
	srv := newTestServer(t)
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
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func assertAllowPost(t *testing.T, resp *http.Response) {
	t.Helper()
	if allow := resp.Header.Get("Allow"); allow != "POST" {
		t.Errorf("expected Allow: POST, got %q", allow)
	}
}

func TestHTTPServer_sessionPersistsProjection(t *testing.T) {
	srv, ts := newHTTPTestServer(t)
	fake := fakeConn("get_item")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"{\"id\":1,\"secret\":\"x\"}"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake) //nolint:errcheck
	sessionID := initSession(t, ts)
	setSessionProjection(t, ts, sessionID, "svc", "get_item", map[string]any{"include": []string{"id"}})
	text := httpExecToolText(t, ts, sessionID, "svc", "get_item")
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
	assertAllowPost(t, resp)
}

func TestHTTPServer_methodNotAllowed(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	resp := requestMCPMethod(t, ts, http.MethodGet)
	defer resp.Body.Close()
	mustStatus(t, resp, http.StatusMethodNotAllowed)
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
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id + 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"server": "svc", "tool": "ping"}},
	})
	resp := mcpPost(t, ts, body, "")
	if resp.StatusCode != http.StatusOK {
		errs <- "unexpected status"
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
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

func ssePost(t *testing.T, ts *httptest.Server) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "list", "arguments": map[string]any{}},
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHTTPServer_SSEResponse(t *testing.T) {
	srv, ts := newHTTPTestServer(t)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fakeConn("myTool"))
	resp := ssePost(t, ts)
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

func TestHTTPServer_SessionID(t *testing.T) {
	ts := httptest.NewServer(newTestServer(t))
	defer ts.Close()

	check := func(t *testing.T, id string, want int) {
		t.Helper()
		resp := mcpPost(t, ts, initRequest(), id)
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("id=%q: got %d, want %d", id, resp.StatusCode, want)
		}
	}

	t.Run("valid UUID", func(t *testing.T) { check(t, "abcdef01-2345-6789-abcd-ef0123456789", 200) })
	t.Run("short ID", func(t *testing.T) { check(t, "a", 400) })
	t.Run("32 hyphens", func(t *testing.T) { check(t, "--------------------------------", 400) })
	t.Run("31 hex chars", func(t *testing.T) { check(t, "abcdef0123456789abcdef012345678", 400) })
	t.Run("32 hex chars", func(t *testing.T) { check(t, "abcdef0123456789abcdef0123456789", 200) })
}

func TestHTTPServer_GetAllowHeader(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			resp := requestMCPMethod(t, ts, method)
			resp.Body.Close()
			mustStatus(t, resp, http.StatusMethodNotAllowed)
			assertAllowPost(t, resp)
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

func TestHTTPServer_BodyLimitRejected(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	// 1MB + 1 byte — just over the limit
	oversized := make([]byte, 1<<20+1)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// Should be 200 with a JSON-RPC error body (not 413, by design)
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body) //nolint:errcheck
	if body["error"] == nil {
		t.Errorf("expected JSON-RPC error for oversized body, got: %v", body)
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
