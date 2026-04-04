//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

type errAfterRegisterConn struct {
	tools []transport.ToolDefinition
	errFn func() error
}

func (c *errAfterRegisterConn) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if err := c.errFn(); err != nil {
		return nil, err
	}
	return json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`), nil
}
func (c *errAfterRegisterConn) ListTools(_ context.Context) ([]transport.ToolDefinition, error) {
	return c.tools, nil
}
func (c *errAfterRegisterConn) Health(_ context.Context) error { return nil }
func (c *errAfterRegisterConn) Close() error                   { return nil }

func TestReconnect_rpcErrorDoesNotTriggerReconnect(t *testing.T) {
	srv := newTestServer(t)
	errConn := &errAfterRegisterConn{
		tools: []transport.ToolDefinition{
			{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{}`)},
		},
		errFn: func() error { return &transport.RPCError{Code: -32602, Message: "bad args"} },
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, errConn)
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "ping", "params": map[string]any{},
	}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["ok"] != false {
		t.Errorf("expected ok=false for RPC error, got: %v", env)
	}
	if srv.IsReconnecting("svc") {
		t.Error("RPC error should not trigger reconnect")
	}
}

func fakeConnWithResp(names ...string) *transport.FakeConnection {
	fc := fakeConn(names...)
	fc.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	return fc
}

func TestReconnect_changedToolSet_noStaleEntries(t *testing.T) {
	srv, ctx := newTestServer(t), context.Background()
	fakeV1 := fakeConnWithResp("toolA", "toolB")
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, fakeV1)
	serve(t, srv, callTool("config", map[string]any{"action": "remove_server", "server": "svc"}))
	fakeV2 := fakeConnWithResp("toolA")
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, fakeV2)
	t.Run("toolA succeeds", func(t *testing.T) {
		resp := serve(t, srv, callTool("call", map[string]any{
			"server": "svc", "tool": "toolA", "params": map[string]any{},
		}))
		if resp["error"] != nil {
			t.Errorf("unexpected error for toolA: %v", resp["error"])
		}
	})
	t.Run("toolB not found", func(t *testing.T) {
		resp := serve(t, srv, callTool("call", map[string]any{
			"server": "svc", "tool": "toolB", "params": map[string]any{},
		}))
		if text := toolResultText(t, resp); text == "" {
			t.Error("expected an error message for missing toolB")
		}
	})
}

func reconnectHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newMCPTestServer(t, []map[string]any{
		{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
	})
}

func waitForReconnect(t *testing.T, fakeClock *clock.Fake, srv *server.Server, svc string) {
	t.Helper()
	reconnected := make(chan struct{})
	srv.SetReconnectHook(svc, func() { close(reconnected) })
	if err := fakeClock.BlockUntilContext(t.Context(), 1); err != nil {
		t.Fatalf("waiting for reconnect timer: %v", err)
	}
	fakeClock.Advance(time.Second)
	select {
	case <-reconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reconnect to complete")
	}
}

func newReconnectSrv(t *testing.T) (*server.Server, *clock.Fake) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	fakeClock := clock.NewFake(time.Now())
	return server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithClock(fakeClock)), fakeClock
}

func makeErrConn(errOnCall *bool) *errAfterRegisterConn {
	return &errAfterRegisterConn{
		tools: []transport.ToolDefinition{
			{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{}`)},
		},
		errFn: func() error {
			if *errOnCall {
				return fmt.Errorf("broken pipe: %w", errors.New("connection reset"))
			}
			return nil
		},
	}
}

func assertEnvelopeOK(t *testing.T, srv *server.Server, svcName, toolName string, wantOK bool) {
	t.Helper()
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": svcName, "tool": toolName, "params": map[string]any{},
	}))
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["ok"] != wantOK {
		t.Errorf("expected ok=%v, got: %v", wantOK, env)
	}
}

func TestReconnect_successAfterFailure(t *testing.T) {
	httpSrv := reconnectHTTPServer(t)
	defer httpSrv.Close()
	srv, fakeClock := newReconnectSrv(t)
	var errOnCall bool
	srv.AddConnection(context.Background(), config.ServerConfig{
		Name: "svc", Transport: "http", URL: httpSrv.URL,
	}, makeErrConn(&errOnCall))
	errOnCall = true
	assertEnvelopeOK(t, srv, "svc", "ping", false)
	errOnCall = false
	waitForReconnect(t, fakeClock, srv, "svc")
	assertEnvelopeOK(t, srv, "svc", "ping", true)
}

func writeToolsCallResp(w http.ResponseWriter, id any, callsSeen int, rpcErrOnCall *int) {
	if *rpcErrOnCall > 0 && callsSeen == *rpcErrOnCall {
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": "bad args"}}) //nolint:errcheck
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, //nolint:errcheck
		"result": map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}})
}

func perSessionUpstreamHandler(dialCount *int, rpcErrOnCall *int) http.HandlerFunc {
	callsSeen := 0
	return func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		id := req["id"]
		switch req["method"] {
		case "initialize":
			*dialCount++
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, //nolint:errcheck
				"result": map[string]any{"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{"tools": map[string]any{}},
					"serverInfo":   map[string]any{"name": "fake", "version": "0"}}})
		case "tools/list":
			tools := []map[string]any{{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}}}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": tools}}) //nolint:errcheck
		case "tools/call":
			callsSeen++
			writeToolsCallResp(w, id, callsSeen, rpcErrOnCall)
		}
	}
}

func perSessionHTTPServer(t *testing.T, dialCount *int, rpcErrOnCall *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(perSessionUpstreamHandler(dialCount, rpcErrOnCall))
	t.Cleanup(srv.Close)
	return srv
}

func newPerSessionSrv(t *testing.T, url string) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sc := config.ServerConfig{Name: "svc", Transport: "http", URL: url, SessionMode: config.SessionModePerSession}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("AddUpstream: %v", err)
	}
	return srv
}

func postToMiniHTTP(t *testing.T, ts *httptest.Server, sessionID string, body []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("mini POST: %v", err)
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
}

func TestPerSession_rpcErrorKeepsConn(t *testing.T) {
	var dialCount int
	rpcErrOnCall := 2 // inject RPC error on the 2nd tools/call
	upstreamSrv := perSessionHTTPServer(t, &dialCount, &rpcErrOnCall)
	ts := httptest.NewServer(newPerSessionSrv(t, upstreamSrv.URL))
	t.Cleanup(ts.Close)
	baseline := dialCount
	sid := "aabbccdd11223344aabbccdd11223344"

	init, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"}}})
	exec, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"server": "svc", "tool": "ping"}}})
	postToMiniHTTP(t, ts, sid, init)
	postToMiniHTTP(t, ts, sid, exec) // call 1: success, triggers per-session dial
	postToMiniHTTP(t, ts, sid, exec) // call 2: RPC error
	postToMiniHTTP(t, ts, sid, exec) // call 3: should reuse conn, not redial

	if dialCount != baseline+1 {
		t.Errorf("RPC error should not redial per-session conn: expected %d dials, got %d", baseline+1, dialCount)
	}
}
