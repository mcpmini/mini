//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestServeReturnsFirstResponseWriteError(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	want := errors.New("broken output")
	var writes atomic.Int32
	out := failingOutput{write: func([]byte) (int, error) {
		writes.Add(1)
		return 0, want
	}}
	input := append(buildServeInput(nil), []byte("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"ping\"}\n")...)
	if err := srv.Serve(t.Context(), bytes.NewReader(input), out); !errors.Is(err, want) {
		t.Fatalf("Serve error = %v, want output failure cause %v", err, want)
	}
	if got := writes.Load(); got != 1 {
		t.Fatalf("output writes = %d, want 1", got)
	}
}

func TestServeOutputFailureCancelsAndJoinsAcceptedCall(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	callStarted, callFinished := make(chan struct{}), make(chan struct{})
	conn := &outputFailureConnection{started: callStarted, finished: callFinished}
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	want := errors.New("output failed")
	var writes atomic.Int32
	out := failingOutput{write: func(p []byte) (int, error) {
		if writes.Add(1) == 2 {
			<-callStarted
			return 0, want
		}
		return len(p), nil
	}}
	input := buildServeInput([][]byte{
		callTool("svc__op", map[string]any{}),
		rpc("ping", nil),
	})
	if err := srv.Serve(t.Context(), bytes.NewReader(input), out); !errors.Is(err, want) {
		t.Fatalf("Serve error = %v, want output failure cause %v", err, want)
	}
	select {
	case <-callFinished:
	default:
		t.Fatal("Serve returned before the accepted call finished")
	}
}

type failingOutput struct{ write func([]byte) (int, error) }

func (w failingOutput) Write(p []byte) (int, error) { return w.write(p) }

type outputFailureConnection struct {
	started  chan struct{}
	finished chan struct{}
	once     sync.Once
}

func (c *outputFailureConnection) Call(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	c.once.Do(func() { close(c.started) })
	<-ctx.Done()
	close(c.finished)
	return nil, ctx.Err()
}

func (*outputFailureConnection) ListTools(context.Context) (transport.ToolList, error) {
	return transport.ToolList{Tools: []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}}}, nil
}

func (*outputFailureConnection) Health(context.Context) error { return nil }

func (*outputFailureConnection) Close() error { return nil }

func TestMalformedJSONLine(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	lines := rawServe(t, srv, []byte("not json at all\n"))
	if len(lines) == 0 {
		t.Fatal("expected at least one response line")
	}
	resp := parseRPCResponse(t, lines[0])
	if resp["error"] == nil {
		t.Errorf("expected error for malformed JSON, got: %v", resp)
	}
	rpcErr := resp["error"].(map[string]any)
	if rpcErr["code"] == nil {
		t.Errorf("expected error code, got: %v", rpcErr)
	}
}

func TestEmptyLines_skipped(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	// Empty lines should be silently skipped, not produce error responses
	input := "\n\n" + string(rpc("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}))
	lines := rawServe(t, srv, []byte(input))
	if len(lines) != 1 {
		t.Errorf("expected exactly 1 response (for initialize), got %d: %s", len(lines), bytes.Join(lines, []byte("|")))
	}
}

// TestBatchRequest_returnsParseError verifies that a JSON array (batch) on a single line
// returns a -32700 Parse Error. mini does not implement batch support; the spec says
// initialize MUST NOT be in a batch, and other batches are valid but optional to support.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/transports.mdx#L25
func TestBatchRequest_returnsParseError(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	batch := []byte(`[{"jsonrpc":"2.0","id":1,"method":"ping"}]` + "\n")
	lines := rawServe(t, srv, batch)
	if len(lines) == 0 {
		t.Fatal("expected a response")
	}
	resp := parseRPCResponse(t, lines[0])
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error for batch request, got: %v", resp)
	}
	if code := int(errObj["code"].(float64)); code != transport.CodeParseError {
		t.Errorf("batch should return -32700 ParseError, got code %d", code)
	}
}

func TestServer_closeStopsServe(t *testing.T) {
	srv := newTestServer(t, server.Params{})
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
