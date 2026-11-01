//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestHTTPServer_getWithoutSSEAcceptReturns406(t *testing.T) {
	_, ts := newHTTPTestServer(t, server.Params{})
	resp := requestMCPMethod(t, ts, http.MethodGet)
	defer resp.Body.Close()
	mustStatus(t, resp, http.StatusNotAcceptable)
}

func TestHTTPServer_getStreamRejectsSessionsItCannotServe(t *testing.T) {
	_, ts := newHTTPTestServer(t, server.Params{})
	cases := []struct {
		name      string
		sessionID string
		want      int
	}{
		{name: "missing session ID", sessionID: "", want: http.StatusBadRequest},
		{name: "malformed session ID", sessionID: "short", want: http.StatusBadRequest},
		{name: "unknown session", sessionID: "6f1c2a9e-4b7d-4c1e-9a3f-2d8e5b7c1a04", want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := requestEventStream(t, ts, tc.sessionID)
			defer resp.Body.Close()
			mustStatus(t, resp, tc.want)
		})
	}
}

func TestHTTPServer_SSEResponse(t *testing.T) {
	srv, ts := newHTTPTestServer(t, server.Params{})
	fake := fakeConn("myTool")
	fake.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake) //nolint:errcheck
	sessionID := initSession(t, ts)
	resp := sseProxyToolCall(
		sseProxyToolCallParams{T: t, HTTPServer: ts, SessionID: sessionID, ToolName: "svc__myTool"},
	)
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
	_, ts := newHTTPTestServer(t, server.Params{})
	resp := requestToolsList(t, ts, "application/json")
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type: application/json, got: %q", ct)
	}
}

func TestHTTPServer_SSEWithBothAcceptTypes(t *testing.T) {
	_, ts := newHTTPTestServer(t, server.Params{})
	resp := requestToolsList(t, ts, "application/json, text/event-stream")
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected SSE when Accept includes text/event-stream, got: %q", ct)
	}
}

func TestHTTPServer_GetAllowHeader(t *testing.T) {
	_, ts := newHTTPTestServer(t, server.Params{})
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			resp := requestMCPMethod(t, ts, method)
			resp.Body.Close()
			mustStatus(t, resp, http.StatusMethodNotAllowed)
			assertAllowMethods(t, resp)
		})
	}
}

func TestHTTPServer_SSEXAccelBuffering(t *testing.T) {
	_, ts := newHTTPTestServer(t, server.Params{})
	resp := requestToolsList(t, ts, "text/event-stream")
	defer resp.Body.Close()
	if v := resp.Header.Get("X-Accel-Buffering"); v != "no" {
		t.Errorf("expected X-Accel-Buffering: no, got %q", v)
	}
}

func requestToolsList(t *testing.T, ts *httptest.Server, accept string) *http.Response {
	t.Helper()
	sessionID := initSession(t, ts)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": transport.JsonRPCVersion, "id": 1, "method": "tools/list",
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

func requestEventStream(t *testing.T, ts *httptest.Server, sessionID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func sseProxyToolCall(params sseProxyToolCallParams) *http.Response {
	params.T.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": transport.JsonRPCVersion, "id": 1, "method": "tools/call",
		"params": map[string]any{"name": params.ToolName, "arguments": map[string]any{}},
	})
	req, _ := http.NewRequest(http.MethodPost, params.HTTPServer.URL+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", params.SessionID)
	resp, err := params.HTTPServer.Client().Do(req)
	if err != nil {
		params.T.Fatal(err)
	}
	return resp
}

type sseProxyToolCallParams struct {
	T          *testing.T
	HTTPServer *httptest.Server
	SessionID  string
	ToolName   string
}
