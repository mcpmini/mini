package server_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

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

func TestQueueDepth_rejectsWhenFull(t *testing.T) {
	srv := newFailureServer(t)
	blocked, release := make(chan struct{}), make(chan struct{})
	slow := newBlockingConn(blocked, release)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc", MaxPendingRequests: 1}, slow)
	go func() {
		serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "op", "params": map[string]any{}}))
	}()
	<-blocked
	resp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "op", "params": map[string]any{}}))
	text := toolResultText(t, resp)
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("expected JSON envelope: %s", text)
	}
	if env["ok"] != false {
		t.Errorf("expected ok=false for queue-full rejection, got: %v", env)
	}
	close(release)
}

func TestQueueDepth_zeroMeansUnlimited(t *testing.T) {
	srv := newFailureServer(t)
	ctx := context.Background()

	fake := &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{"tools/call": toolJSON(`{"result":"ok"}`)},
	}

	sc := config.ServerConfig{Name: "svc", MaxPendingRequests: 0}
	srv.AddConnection(ctx, sc, fake)

	for i := 0; i < 5; i++ {
		resp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "op", "params": map[string]any{}}))
		text := toolResultText(t, resp)
		var env map[string]any
		if err := json.Unmarshal([]byte(text), &env); err != nil {
			t.Fatalf("call %d: expected JSON envelope: %s", i, text)
		}
		if env["ok"] != true {
			t.Errorf("call %d: expected ok=true with unlimited queue, got: %v", i, env)
		}
	}
}

func assertQueueCallOK(t *testing.T, srv *server.Server, callN int) {
	t.Helper()
	resp := serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "op", "params": map[string]any{}}))
	var env map[string]any
	json.Unmarshal([]byte(toolResultText(t, resp)), &env)
	if env["ok"] != true {
		t.Errorf("call %d should succeed, got: %v", callN, env)
	}
}

func TestQueueDepth_slotReleasedAfterCompletion(t *testing.T) {
	srv := newFailureServer(t)
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
	assertQueueCallOK(t, srv, 1)
	assertQueueCallOK(t, srv, 2)
}

func TestQueueDepth_timeoutReleasesSlot(t *testing.T) {
	srv := newFailureServer(t)
	ctx := context.Background()
	slow := &ctxAwareConnection{tools: []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}}}
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc", MaxPendingRequests: 1, ToolTimeout: "50ms"}, slow)
	serve(t, srv, callTool("call", map[string]any{"server": "svc", "tool": "op", "params": map[string]any{}}))
	slow2 := &ctxAwareConnection{
		tools:  []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}},
		result: toolJSON(`{"result":"ok"}`),
	}
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc2", MaxPendingRequests: 1}, slow2)
	resp := serve(t, srv, callTool("call", map[string]any{"server": "svc2", "tool": "op", "params": map[string]any{}}))
	var env map[string]any
	json.Unmarshal([]byte(toolResultText(t, resp)), &env)
	if env["ok"] != true {
		t.Errorf("svc2 should work normally, got: %v", env)
	}
}

func toolJSON(s string) json.RawMessage {
	result, _ := json.Marshal(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": s}},
	})
	return result
}

// callbackConnection invokes a function on each Call(), ignoring context.
type callbackConnection struct {
	tools []transport.ToolDefinition
	call  func() (json.RawMessage, error)
}

func (c *callbackConnection) ListTools(_ context.Context) ([]transport.ToolDefinition, error) {
	return c.tools, nil
}

func (c *callbackConnection) Call(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return c.call()
}

func (c *callbackConnection) Health(_ context.Context) error { return nil }
func (c *callbackConnection) Close() error                   { return nil }

// ctxAwareConnection blocks until the call context is canceled (or returns a
// fixed result immediately if result is non-nil). Simulates a real transport.
type ctxAwareConnection struct {
	tools  []transport.ToolDefinition
	result json.RawMessage // if non-nil, return immediately
}

func (c *ctxAwareConnection) ListTools(_ context.Context) ([]transport.ToolDefinition, error) {
	return c.tools, nil
}

func (c *ctxAwareConnection) Call(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	if c.result != nil {
		return c.result, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *ctxAwareConnection) Health(_ context.Context) error { return nil }
func (c *ctxAwareConnection) Close() error                   { return nil }
