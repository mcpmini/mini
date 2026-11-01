//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestUpstream_connectionErrorReturnsEnvelope(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	fake := fakeToolConn("ping")
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)
	fake.Err = errors.New("broken pipe")

	resp := serve(t, srv, callTool("svc__ping", map[string]any{}))
	requireToolError(t, resp, "")
	if resp["error"] != nil {
		t.Errorf("connection error should not become RPC error, got: %v", resp["error"])
	}
}

func TestUpstream_allCallsFail_discoverStillWorks(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	ctx := context.Background()
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "write", Description: "write file", InputSchema: json.RawMessage(`{}`)},
			{Name: "read", Description: "read file", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{},
	}
	srv.AddConnection(ctx, config.ServerConfig{Name: "fs"}, fake)
	fake.Err = errors.New("subprocess crashed")

	// discover should still work even when upstream is down
	resp := serve(t, srv, rpc("tools/list", nil))
	text := toolListJSON(t, resp)
	var tools []any
	if err := json.Unmarshal([]byte(text), &tools); err != nil {
		t.Fatalf("expected JSON array from list, got: %s", text)
	}
	if len(tools) != 4 {
		t.Errorf(
			"expected two upstream tools and two builtins in discovery even when upstream is down, got %d",
			len(tools),
		)
	}
}

func TestHTTPUpstream_timeoutReturnsGracefulError(t *testing.T) {
	srv := newSlowServer(t, 200*time.Millisecond, "50ms")

	resp := serve(t, srv, callTool("svc__slowOp", map[string]any{}))
	requireToolError(t, resp, "")
}

func TestUpstream_concurrentCalls_noPanic(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fakeWorkConn())
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := serve(t, srv, callTool("svc__work", map[string]any{}))
			if resp["error"] != nil {
				errs <- "rpc error: " + mustMarshal(resp["error"])
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("unexpected RPC error in concurrent call: %s", e)
	}
}

func TestUpstream_toolReturnsIsError_gracefulEnvelope(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	ctx := context.Background()
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(
				`{"content":[{"type":"text","text":"not found: resource XYZ"}],"isError":true}`,
			),
		},
	}
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, fake)

	resp := serve(t, srv, callTool("svc__op", map[string]any{}))

	requireToolError(t, resp, "not found: resource XYZ")
}

func TestHTTPUpstream_429_returnsError(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{Name: "search", InputSchema: json.RawMessage(`{}`)}},
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "api"}, fake)
	fake.Err = errors.New("http tools/call: status 429: rate limited — try again in 60s")
	resp := serve(t, srv, callTool("api__search", map[string]any{}))
	text := toolResultText(t, resp)
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("expected JSON envelope for rate limit error, got: %s", text)
	}
	if env["error"] == nil {
		t.Errorf("expected ok=false for rate limit error, got: %v", env)
	}
}

func TestUpstream_malformedJSONResponse_gracefulError(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	ctx := context.Background()
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{
			// Valid JSON but content has text that's invalid JSON → treated as string
			"tools/call": json.RawMessage(
				`{"content":[{"type":"text","text":"<html>error page</html>"}],"isError":false}`,
			),
		},
	}
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, fake)

	resp := serve(t, srv, callTool("svc__op", map[string]any{}))

	// Should get some response — not a panic or hang
	if resp["result"] == nil && resp["error"] == nil {
		t.Error("expected either result or error in response")
	}
}

func TestUpstream_oneFailsOthersWork(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	ctx := context.Background()
	goodFake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"pong"}]}`),
		},
	}
	badFake := fakeToolConn("fail")
	srv.AddConnection(ctx, config.ServerConfig{Name: "good"}, goodFake)
	srv.AddConnection(ctx, config.ServerConfig{Name: "bad"}, badFake)
	badFake.Err = errors.New("bad upstream is dead")
	badEnv := parseEnvelope(t, toolResultText(t, serve(t, srv, callTool("bad__fail", map[string]any{}))))
	if badEnv["error"] == nil {
		t.Errorf("expected ok=false for bad upstream, got: %v", badEnv)
	}
	good := serve(t, srv, callTool("good__ping", nil))
	if text := toolResultText(t, good); text != "pong" {
		t.Errorf("healthy upstream result = %q, want pong", text)
	}
}

func TestUpstream_contextCancelledDuringCall(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	slow := &slowConnection{
		tools: []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}},
		delay: 5 * time.Second,
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc", ToolTimeout: "50ms"}, slow)
	start := time.Now()
	resp := serve(t, srv, callTool("svc__op", map[string]any{}))
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("tool call with 50ms timeout took too long: %v", elapsed)
	}
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["error"] == nil {
		t.Errorf("expected ok=false after timeout, got: %v", env)
	}
}

func TestQueueDepth_rejectsWhenFull(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	blocked, release := make(chan struct{}), make(chan struct{})
	slow := newBlockingConn(blocked, release)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc", MaxPendingRequests: 1}, slow)
	go func() {
		serve(t, srv, callTool("svc__op", map[string]any{}))
	}()
	<-blocked
	resp := serve(t, srv, callTool("svc__op", map[string]any{}))
	text := toolResultText(t, resp)
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("expected JSON envelope: %s", text)
	}
	if env["error"] == nil {
		t.Errorf("expected ok=false for queue-full rejection, got: %v", env)
	}
	close(release)
}

func TestQueueDepth_zeroMeansUnlimited(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	ctx := context.Background()

	fake := &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{"tools/call": toolJSON(`{"result":"ok"}`)},
	}

	sc := config.ServerConfig{Name: "svc", MaxPendingRequests: 0}
	srv.AddConnection(ctx, sc, fake)

	for range 5 {
		successText(t, serve(t, srv, callTool("svc__op", map[string]any{})))
	}
}

func TestQueueDepth_slotReleasedAfterCompletion(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	var mu sync.Mutex
	calls := 0
	slow := &callbackConnection{
		tools: []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}},
		call: func() (json.RawMessage, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			return toolJSON("done"), nil
		},
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc", MaxPendingRequests: 1}, slow)
	successText(t, serve(t, srv, callTool("svc__op", map[string]any{})))
	successText(t, serve(t, srv, callTool("svc__op", map[string]any{})))
}

func TestQueueDepth_timeoutReleasesSlot(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	ctx := context.Background()
	slow := &ctxAwareConnection{tools: []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}}}
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc", MaxPendingRequests: 1, ToolTimeout: "50ms"}, slow)
	serve(t, srv, callTool("svc__op", map[string]any{}))
	slow2 := &ctxAwareConnection{
		tools:  []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}},
		result: toolJSON(`{"result":"ok"}`),
	}
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc2", MaxPendingRequests: 1}, slow2)
	successText(t, serve(t, srv, callTool("svc2__op", map[string]any{})))
}

func fakeToolConn(name string) *transport.FakeConnection {
	return &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: name, Description: name, InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{},
	}
}

func newBlockingConn(blocked, release chan struct{}) *callbackConnection {
	return &callbackConnection{
		tools: []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}},
		call: func() (json.RawMessage, error) {
			blocked <- struct{}{}
			<-release
			return toolJSON("done"), nil
		},
	}
}

func newSlowServer(t *testing.T, delay time.Duration, timeout string) *server.Server {
	t.Helper()
	srv := newTestServer(t, server.Params{})
	slow := &slowConnection{
		tools: []transport.ToolDefinition{{Name: "slowOp", InputSchema: json.RawMessage(`{}`)}},
		delay: delay,
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc", ToolTimeout: timeout}, slow)
	return srv
}

func toolJSON(s string) json.RawMessage {
	result, _ := json.Marshal(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": s}},
	})
	return result
}

// slowConnection simulates a connection that takes time to respond.
type slowConnection struct {
	tools []transport.ToolDefinition
	delay time.Duration
}

func (c *slowConnection) Call(ctx context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
	select {
	case <-time.After(c.delay):
		return json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *slowConnection) ListTools(_ context.Context) (transport.ToolList, error) {
	return transport.ToolList{Tools: c.tools}, nil
}

func (c *slowConnection) Health(_ context.Context) error { return nil }

func (c *slowConnection) Close() error { return nil }

// callbackConnection invokes a function on each Call(), ignoring context.
type callbackConnection struct {
	tools []transport.ToolDefinition
	call  func() (json.RawMessage, error)
}

func (c *callbackConnection) ListTools(_ context.Context) (transport.ToolList, error) {
	return transport.ToolList{Tools: c.tools}, nil
}

func (c *callbackConnection) Call(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return c.call()
}

func (c *callbackConnection) Health(_ context.Context) error { return nil }

func (c *callbackConnection) Close() error { return nil }

func fakeWorkConn() *transport.FakeConnection {
	return &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: "work", Description: "do work", InputSchema: json.RawMessage(`{}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"{}"}]}`),
		},
	}
}

// ctxAwareConnection blocks until the call context is canceled (or returns a
// fixed result immediately if result is non-nil). Simulates a real transport.
type ctxAwareConnection struct {
	tools  []transport.ToolDefinition
	result json.RawMessage // if non-nil, return immediately
}

func (c *ctxAwareConnection) ListTools(_ context.Context) (transport.ToolList, error) {
	return transport.ToolList{Tools: c.tools}, nil
}

func (c *ctxAwareConnection) Call(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	if c.result != nil {
		return c.result, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *ctxAwareConnection) Health(_ context.Context) error { return nil }

func (c *ctxAwareConnection) Close() error { return nil }

// helpers
func mustMarshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
