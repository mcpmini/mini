//go:build test

// End-to-end tests for failure modes: connection drops, timeouts, rate limits,
// and upstream instability. These test the proxy's resilience from the
// agent's perspective — what does the agent actually receive when things go wrong?
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func newFailureServer(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	cfg.InlineThreshold = 10000
	return server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func fakeToolConn(name string) *transport.FakeConnection {
	return &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: name, Description: name, InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{},
	}
}

func assertEnvelopeOkFalse(t *testing.T, text string) {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("expected JSON envelope, got: %s", text)
	}
	if env["ok"] != false {
		t.Errorf("expected ok=false, got: %v", env)
	}
}

func TestUpstream_connectionErrorReturnsEnvelope(t *testing.T) {
	srv := newFailureServer(t)
	fake := fakeToolConn("ping")
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fake)
	fake.Err = errors.New("broken pipe")

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "ping", "params": map[string]any{},
	}))
	assertEnvelopeOkFalse(t, toolResultText(t, resp))
	if resp["error"] != nil {
		t.Errorf("connection error should not become RPC error, got: %v", resp["error"])
	}
}

func TestUpstream_allCallsFail_discoverStillWorks(t *testing.T) {
	srv := newFailureServer(t)
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
	resp := serve(t, srv, callTool("list", map[string]any{}))
	text := toolResultText(t, resp)
	var tools []any
	if err := json.Unmarshal([]byte(text), &tools); err != nil {
		t.Fatalf("expected JSON array from list, got: %s", text)
	}
	if len(tools) != 2 {
		t.Errorf("expected 2 tools in discover even when upstream is down, got %d", len(tools))
	}
}

func newSlowServer(t *testing.T, delay time.Duration, timeout string) *server.Server {
	t.Helper()
	srv := newFailureServer(t)
	slow := &slowConnection{
		tools: []transport.ToolDefinition{{Name: "slowOp", InputSchema: json.RawMessage(`{}`)}},
		delay: delay,
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc", ToolTimeout: timeout}, slow)
	return srv
}

func TestHTTPUpstream_timeoutReturnsGracefulError(t *testing.T) {
	srv := newSlowServer(t, 200*time.Millisecond, "50ms")

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "slowOp", "params": map[string]any{},
	}))
	assertEnvelopeOkFalse(t, toolResultText(t, resp))
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

func (c *slowConnection) ListTools(_ context.Context) ([]transport.ToolDefinition, error) {
	return c.tools, nil
}

func (c *slowConnection) Health(_ context.Context) error { return nil }
func (c *slowConnection) Close() error                   { return nil }

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

func TestUpstream_concurrentCalls_noPanic(t *testing.T) {
	srv := newFailureServer(t)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fakeWorkConn())
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := serve(t, srv, callTool("call", map[string]any{
				"server": "svc", "tool": "work", "params": map[string]any{},
			}))
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
	srv := newFailureServer(t)
	ctx := context.Background()
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"not found: resource XYZ"}],"isError":true}`),
		},
	}
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "op", "params": map[string]any{},
	}))

	text := toolResultText(t, resp)
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("expected JSON envelope for tool error, got: %s", text)
	}
	if env["ok"] != false {
		t.Errorf("expected ok=false when upstream tool returns isError, got: %v", env)
	}
}

func TestHTTPUpstream_429_returnsError(t *testing.T) {
	srv := newFailureServer(t)
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{Name: "search", InputSchema: json.RawMessage(`{}`)}},
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "api"}, fake)
	fake.Err = errors.New("http tools/call: status 429: rate limited — try again in 60s")
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "api", "tool": "search", "params": map[string]any{},
	}))
	text := toolResultText(t, resp)
	var env map[string]any
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("expected JSON envelope for rate limit error, got: %s", text)
	}
	if env["ok"] != false {
		t.Errorf("expected ok=false for rate limit error, got: %v", env)
	}
}

func TestUpstream_malformedJSONResponse_gracefulError(t *testing.T) {
	srv := newFailureServer(t)
	ctx := context.Background()
	fake := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{
			// Valid JSON but content has text that's invalid JSON → treated as string
			"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"<html>error page</html>"}],"isError":false}`),
		},
	}
	srv.AddConnection(ctx, config.ServerConfig{Name: "svc"}, fake)

	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "op", "params": map[string]any{},
	}))

	// Should get some response — not a panic or hang
	if resp["result"] == nil && resp["error"] == nil {
		t.Error("expected either result or error in response")
	}
}

func TestUpstream_oneFailsOthersWork(t *testing.T) {
	srv := newFailureServer(t)
	ctx := context.Background()
	goodFake := &transport.FakeConnection{
		Tools:     []transport.ToolDefinition{{Name: "ping", Description: "ping", InputSchema: json.RawMessage(`{}`)}},
		Responses: map[string]json.RawMessage{"tools/call": json.RawMessage(`{"content":[{"type":"text","text":"pong"}]}`)},
	}
	badFake := fakeToolConn("fail")
	srv.AddConnection(ctx, config.ServerConfig{Name: "good"}, goodFake)
	srv.AddConnection(ctx, config.ServerConfig{Name: "bad"}, badFake)
	badFake.Err = errors.New("bad upstream is dead")
	badEnv := parseEnvelope(t, toolResultText(t, serve(t, srv, callTool("call", map[string]any{
		"server": "bad", "tool": "fail", "params": map[string]any{},
	}))))
	if badEnv["ok"] != false {
		t.Errorf("expected ok=false for bad upstream, got: %v", badEnv)
	}
	goodEnv := parseEnvelope(t, toolResultText(t, serve(t, srv, callTool("call", map[string]any{
		"server": "good", "tool": "ping", "params": map[string]any{},
	}))))
	if goodEnv["ok"] != true {
		t.Errorf("expected ok=true for good upstream after bad upstream failed, got: %v", goodEnv)
	}
}

func TestServer_closeStopsServe(t *testing.T) {
	srv := newFailureServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, pr, &out) }()
	initReq := rpc("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "t", "version": "0"},
	})
	pw.Write(initReq) //nolint:errcheck
	cancel()
	pw.Close()
	select {
	case err := <-done:
		_ = err
	case <-time.After(2 * time.Second):
		t.Error("Serve did not exit after context cancel + pipe close")
	}
}

func TestUpstream_contextCancelledDuringCall(t *testing.T) {
	srv := newFailureServer(t)
	slow := &slowConnection{
		tools: []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}},
		delay: 5 * time.Second,
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc", ToolTimeout: "50ms"}, slow)
	start := time.Now()
	resp := serve(t, srv, callTool("call", map[string]any{
		"server": "svc", "tool": "op", "params": map[string]any{},
	}))
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("tool call with 50ms timeout took too long: %v", elapsed)
	}
	env := parseEnvelope(t, toolResultText(t, resp))
	if env["ok"] != false {
		t.Errorf("expected ok=false after timeout, got: %v", env)
	}
}

// helpers

func mustMarshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
