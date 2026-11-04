//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/mcptest"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/testutil"
	"github.com/mcpmini/mini/internal/transport"
)

type errAfterRegisterConn struct {
	tools []transport.ToolDefinition
	errFn func() error
}

func (c *errAfterRegisterConn) Call(
	ctx context.Context,
	method string,
	params json.RawMessage,
) (json.RawMessage, error) {
	if err := c.errFn(); err != nil {
		return nil, err
	}
	return json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`), nil
}

func (c *errAfterRegisterConn) ListTools(_ context.Context) (transport.ToolList, error) {
	return transport.ToolList{Tools: c.tools}, nil
}

func (c *errAfterRegisterConn) Health(_ context.Context) error { return nil }

func (c *errAfterRegisterConn) Close() error { return nil }

func TestReconnect_rpcErrorDoesNotTriggerReconnect(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	errConn := &errAfterRegisterConn{
		tools: []transport.ToolDefinition{
			{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{}`)},
		},
		errFn: func() error { return &transport.RPCError{Code: -32602, Message: "bad args"} },
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, errConn)
	resp := serve(t, srv, callTool("svc__ping", map[string]any{}))
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
	srv, ctx := newTestServer(t, server.Params{}), context.Background()
	fakeV1 := fakeConnWithResp("toolA", "toolB")
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, fakeV1)
	serve(t, srv, callTool("config", map[string]any{"action": "remove_server", "server": "svc"}))
	fakeV2 := fakeConnWithResp("toolA")
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, fakeV2)
	t.Run("toolA succeeds", func(t *testing.T) {
		resp := serve(t, srv, callTool("svc__toolA", map[string]any{}))
		if resp["error"] != nil {
			t.Errorf("unexpected error for toolA: %v", resp["error"])
		}
	})
	t.Run("toolB not found", func(t *testing.T) {
		resp := serve(t, srv, callTool("svc__toolB", map[string]any{}))
		requireRPCError(
			requireRPCErrorParams{T: t, Resp: resp, WantCode: transport.CodeInvalidParams, WantSubstr: "not found"},
		)
	})
}

func reconnectHTTPServer(t *testing.T) *mcptest.Server {
	t.Helper()
	return mcptest.NewHTTP(t, mcptest.Upstream{
		Tools: []mcptest.Tool{{Name: "ping"}},
		Methods: map[string]mcptest.Method{"tools/call": func(mcptest.Request) mcptest.Reply {
			return mcptest.Reply{Result: map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}}
		}},
	})
}

type waitForReconnectParams struct {
	T          *testing.T
	FakeClock  *clock.Fake
	Server     *server.Server
	ServerName string
}

func waitForReconnect(params waitForReconnectParams) {
	params.T.Helper()
	reconnected := make(chan struct{})
	params.Server.SetReconnectHook(params.ServerName, func() { close(reconnected) })
	if err := params.FakeClock.BlockUntilContext(params.T.Context(), responseCleanupTimer+1); err != nil {
		params.T.Fatalf("waiting for reconnect timer: %v", err)
	}
	params.FakeClock.Advance(time.Second)
	testutil.WaitForReceive(params.T, reconnected, "the reconnect to complete")
}

func newReconnectSrv(t *testing.T) (*server.Server, *clock.Fake) {
	t.Helper()
	fakeClock := clock.NewFake()
	return newTestServer(t, server.Params{Clock: fakeClock}), fakeClock
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

type assertCallSuccessParams struct {
	T           *testing.T
	Server      *server.Server
	ServerName  string
	ToolName    string
	WantSuccess bool
}

func assertCallSuccess(params assertCallSuccessParams) {
	params.T.Helper()
	resp := serve(params.T, params.Server, callTool(params.ServerName+"__"+params.ToolName, map[string]any{}))
	result, _ := resp["result"].(map[string]any)
	gotOK := resp["error"] == nil && result != nil && result["isError"] != true
	if gotOK != params.WantSuccess {
		params.T.Errorf("expected success=%v, got: %v", params.WantSuccess, resp)
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
	assertCallSuccess(
		assertCallSuccessParams{T: t, Server: srv, ServerName: "svc", ToolName: "ping", WantSuccess: false},
	)
	errOnCall = false
	waitForReconnect(waitForReconnectParams{T: t, FakeClock: fakeClock, Server: srv, ServerName: "svc"})
	assertCallSuccess(
		assertCallSuccessParams{T: t, Server: srv, ServerName: "svc", ToolName: "ping", WantSuccess: true},
	)
}

// TestClose_concurrentConnError verifies that Server.Close() does not panic when a
// request goroutine encounters a connection error and calls maybeReconnect at the
// same time as Close() is completing its reconnectWg.Wait(). This is a regression
// test for the WaitGroup reuse race: Add(1) called after Wait() unblocked.
func TestClose_concurrentConnError(t *testing.T) {
	// Run many iterations to expose the narrow scheduling window.
	for i := range 50 {
		fakeClock := clock.NewFake()
		srv := newTestServer(t, server.Params{Clock: fakeClock})

		// slow is a connection that blocks until released, then returns a transport error.
		// This simulates a call that errors exactly as Close() is running.
		release := make(chan struct{})
		slow := &slowErrConn{
			tools: []transport.ToolDefinition{
				{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{}`)},
			},
			release: release,
		}
		srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, slow)

		// Start a call that will block until we close the release channel.
		var callErr error
		done := make(chan struct{})
		go func() {
			defer close(done)
			resp := serve(t, srv, callTool("svc__ping", map[string]any{}))
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

func (c *slowErrConn) ListTools(_ context.Context) (transport.ToolList, error) {
	return transport.ToolList{Tools: c.tools}, nil
}

func (c *slowErrConn) Health(_ context.Context) error { return nil }

func (c *slowErrConn) Close() error { return nil }
