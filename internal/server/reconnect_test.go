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
	"sync/atomic"
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
	if env["error"] == nil {
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
		requireToolError(t, resp, "not found")
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
	gotOK := env["error"] == nil
	if gotOK != wantOK {
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

// TestPerSession_transportErrorRedialsConn verifies that a transport-level
// error (abrupt connection close, not an RPC error) evicts the per-session
// conn so the next call re-dials instead of reusing a broken connection.
func TestPerSession_transportErrorRedialsConn(t *testing.T) {
	var dialCount, closeOnCall, callsSeen atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		id := req["id"]
		switch req["method"] {
		case "initialize":
			dialCount.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, //nolint:errcheck
				"result": map[string]any{"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{"tools": map[string]any{}},
					"serverInfo":   map[string]any{"name": "fake", "version": "0"}}})
		case "tools/list":
			tools := []map[string]any{{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}}}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": tools}}) //nolint:errcheck
		case "tools/call":
			n := callsSeen.Add(1)
			if c := closeOnCall.Load(); c > 0 && n == c {
				hj, ok := w.(http.Hijacker)
				if !ok {
					t.Error("hijack not supported")
					return
				}
				conn, _, _ := hj.Hijack()
				conn.Close() // abrupt transport close
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, //nolint:errcheck
				"result": map[string]any{"content": []map[string]any{{"type": "text", "text": "pong"}}}})
		}
	})
	upstreamSrv := httptest.NewServer(handler)
	t.Cleanup(upstreamSrv.Close)
	ts := httptest.NewServer(newPerSessionSrv(t, upstreamSrv.URL))
	t.Cleanup(ts.Close)

	sid := "aabbccdd11223344aabbccdd11223344"
	init, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 0, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"}}})
	exec, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"server": "svc", "tool": "ping"}}})

	postToMiniHTTP(t, ts, sid, init)
	postToMiniHTTP(t, ts, sid, exec) // call 1: success, dials per-session conn (dialCount → 1)

	closeOnCall.Store(2)
	postToMiniHTTP(t, ts, sid, exec) // call 2: transport error → conn evicted

	closeOnCall.Store(0)
	postToMiniHTTP(t, ts, sid, exec) // call 3: should redial (dialCount → 2)

	if got := dialCount.Load(); got < 2 {
		t.Errorf("transport error should trigger redial: expected ≥2 dials, got %d", got)
	}
}

// TestClose_concurrentConnError verifies that Server.Close() does not panic when a
// request goroutine encounters a connection error and calls maybeReconnect at the
// same time as Close() is completing its reconnectWg.Wait(). This is a regression
// test for the WaitGroup reuse race: Add(1) called after Wait() unblocked.
func TestClose_concurrentConnError(t *testing.T) {
	// Run many iterations to expose the narrow scheduling window.
	for i := range 50 {
		cfg := config.DefaultConfig()
		cfg.ResponseDir = t.TempDir()
		cfg.InlineThreshold = 10000
		fakeClock := clock.NewFake(time.Now())
		srv := server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), server.WithClock(fakeClock))

		// slow is a connection that blocks until released, then returns a transport error.
		// This simulates a call that errors exactly as Close() is running.
		release := make(chan struct{})
		slow := &slowErrConn{
			tools:   []transport.ToolDefinition{{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{}`)}},
			release: release,
		}
		srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, slow)

		// Start a call that will block until we close the release channel.
		var callErr error
		done := make(chan struct{})
		go func() {
			defer close(done)
			resp := serve(t, srv, callTool("call", map[string]any{
				"server": "svc", "tool": "ping", "params": map[string]any{},
			}))
			if resp["error"] != nil {
				callErr = fmt.Errorf("rpc error: %v", resp["error"])
			}
			_ = callErr
		}()

		// Allow the call to proceed and error, then immediately Close the server.
		// The Close() must not panic even if the error handler fires during shutdown.
		close(release)
		srv.Close()
		<-done

		_ = i
	}
}

// slowErrConn blocks on Call until its release channel is closed, then returns a
// transport-level error (not an RPC error), which triggers maybeReconnect.
type slowErrConn struct {
	tools   []transport.ToolDefinition
	release <-chan struct{}
}

func (c *slowErrConn) Call(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	select {
	case <-c.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return nil, errors.New("transport: connection reset")
}
func (c *slowErrConn) ListTools(_ context.Context) ([]transport.ToolDefinition, error) {
	return c.tools, nil
}
func (c *slowErrConn) Health(_ context.Context) error { return nil }
func (c *slowErrConn) Close() error                   { return nil }
