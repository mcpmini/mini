//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

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
	input := append(buildServeInput(false, nil), []byte("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"ping\"}\n")...)
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
	input := buildServeInput(false, [][]byte{
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

func (*outputFailureConnection) ListTools(context.Context) ([]transport.ToolDefinition, error) {
	return []transport.ToolDefinition{{Name: "op", InputSchema: json.RawMessage(`{}`)}}, nil
}

func (*outputFailureConnection) Health(context.Context) error { return nil }
func (*outputFailureConnection) Close() error                 { return nil }
