//go:build test

package server_test

// Cancellation tests verify compliance with the MCP cancellation spec:
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/utilities/cancellation.mdx

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

// TestCancellation_NotificationAccepted verifies that notifications/cancelled
// is accepted (no error response) even for an unknown or already-complete request.
// "Receivers MAY ignore cancellation notifications if the referenced request
// is unknown or processing has already completed."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/utilities/cancellation.mdx#L33
func TestCancellation_NotificationAccepted(t *testing.T) {
	srv := newTestServer(t)
	msgs := serveAll(t, srv,
		notification("notifications/cancelled", map[string]any{
			"requestId": 999,
			"reason":    "user cancelled",
		}),
	)
	for _, m := range msgs {
		if m["id"] == nil {
			continue // ignore notifications
		}
		if m["error"] != nil {
			t.Errorf("notifications/cancelled returned error: %v", m["error"])
		}
	}
}

// TestCancellation_CancelsInFlightCall verifies that a notifications/cancelled
// for an in-flight tools/call actually cancels the upstream call.
// "Receivers SHOULD stop processing the cancelled request and free associated resources."
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/utilities/cancellation.mdx#L23
func TestCancellation_CancelsInFlightCall(t *testing.T) {
	srv := newTestServer(t)

	var callStarted sync.WaitGroup
	callStarted.Add(1)
	var callCtx context.Context
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "s"}, newSlowBlockingConn(&callStarted, &callCtx))

	// Use a pipe so we can write the cancel notification only after the call
	// has registered. Sending both in a single buffer risks the cancel
	// goroutine running before registerInFlight has been called.
	pr, pw := io.Pipe()
	ctx, ctxCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer ctxCancel()
	var out bytes.Buffer
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx, pr, &out) }()

	pw.Write(buildServeInput(true, [][]byte{slowCallReq(42)})) //nolint:errcheck

	if !waitTimeout(&callStarted, 3*time.Second) {
		pw.Close()
		t.Fatal("upstream call did not start within timeout")
	}

	pw.Write(notification("notifications/cancelled", map[string]any{"requestId": 42})) //nolint:errcheck
	pw.Close()

	assertContextCanceled(t, callCtx, "after notifications/cancelled")
	<-serveErr
}

// TestCancellation_CancelsInFlightCall_OverHTTP proves cancellation also works
// over HTTP: all POSTs for one session share a *Session, so a later cancel
// notification reaches the running request's context.
func TestCancellation_CancelsInFlightCall_OverHTTP(t *testing.T) {
	srv, ts := newHTTPTestServer(t)

	var callStarted sync.WaitGroup
	callStarted.Add(1)
	var callCtx context.Context
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "s"}, newSlowBlockingConn(&callStarted, &callCtx))

	sessionID := initSession(t, ts)
	go drainMCPPost(t, ts, slowCallReq(42), sessionID)

	if !waitTimeout(&callStarted, 3*time.Second) {
		t.Fatal("upstream call did not start within timeout")
	}

	drainMCPPost(t, ts, notification("notifications/cancelled", map[string]any{"requestId": 42}), sessionID)

	assertContextCanceled(t, callCtx, "over HTTP")
}

// TestCancellation_UnknownMethodReturnsError verifies that a completely unknown
// method (not a cancellation notification) still returns method-not-found.
func TestCancellation_UnknownMethodReturnsError(t *testing.T) {
	srv := newTestServer(t)
	resp := serve(t, srv, rpc("unknown/method", nil))
	if resp["error"] == nil {
		t.Error("unknown method should return JSON-RPC error, got none")
	}
	errObj, _ := resp["error"].(map[string]any)
	if code, _ := errObj["code"].(float64); int(code) != transport.CodeMethodNotFound {
		t.Errorf("error code = %v, want %d", errObj["code"], transport.CodeMethodNotFound)
	}
}

// --- helpers ---

func notification(method string, params any) []byte {
	p, _ := json.Marshal(params)
	msg := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  json.RawMessage(p),
	}
	b, _ := json.Marshal(msg)
	return append(b, '\n')
}

func callToolWithID(id int, name string, args any) []byte {
	a, _ := json.Marshal(args)
	p, _ := json.Marshal(map[string]any{"name": name, "arguments": json.RawMessage(a)})
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params":  json.RawMessage(p),
	}
	b, _ := json.Marshal(msg)
	return append(b, '\n')
}

// newSlowBlockingConn returns a connection exposing a single "slow" tool
// whose Call blocks until its context is canceled. callStarted is signaled
// once the call begins, and callCtx is set to the context the call observed.
func newSlowBlockingConn(callStarted *sync.WaitGroup, callCtx *context.Context) *blockingFakeConn {
	return &blockingFakeConn{
		tools: []transport.ToolDefinition{{
			Name:        "slow",
			Description: "slow tool",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		onCall: func(ctx context.Context) {
			*callCtx = ctx
			callStarted.Done()
			<-ctx.Done()
		},
	}
}

func slowCallReq(id int) []byte {
	return callToolWithID(id, "call", map[string]any{
		"server": "s",
		"tool":   "slow",
		"params": map[string]any{},
	})
}

func assertContextCanceled(t *testing.T, ctx context.Context, msg string) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Errorf("upstream call context was not canceled %s", msg)
	}
}

func waitTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	ch := make(chan struct{})
	go func() { wg.Wait(); close(ch) }()
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// blockingFakeConn is a transport.Connection whose Call blocks until the
// provided context is cancelled or onCall returns.
type blockingFakeConn struct {
	tools  []transport.ToolDefinition
	onCall func(ctx context.Context)
	mu     sync.Mutex
	closed bool
}

func (f *blockingFakeConn) Call(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	if f.onCall != nil {
		f.onCall(ctx)
	}
	return nil, ctx.Err()
}

func (f *blockingFakeConn) ListTools(_ context.Context) ([]transport.ToolDefinition, error) {
	return f.tools, nil
}

func (f *blockingFakeConn) Health(_ context.Context) error { return nil }

func (f *blockingFakeConn) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}
