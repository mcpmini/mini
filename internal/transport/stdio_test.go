//go:build test

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// makePipeConn creates a StdioConnection backed by in-memory pipes.
// serverW: test writes responses here; serverR: test reads requests here.
func makePipeConn(t *testing.T) (conn *StdioConnection, serverW *io.PipeWriter, serverR io.Reader) {
	t.Helper()
	srvOutR, srvOutW := io.Pipe()
	connOutR, connOutW := io.Pipe()
	conn = newConn(newStdioConnParams{
		cmd: new(exec.Cmd), stdin: connOutW, stdout: srvOutR,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	go conn.readLoop(context.Background())
	t.Cleanup(func() {
		conn.closeDone()
		connOutW.Close()
		srvOutW.Close()
		srvOutR.Close()
		connOutR.Close()
	})
	return conn, srvOutW, connOutR
}

func sendResponse(w io.Writer, id any, result any) {
	raw, _ := json.Marshal(result)
	line, _ := json.Marshal(map[string]any{
		"jsonrpc": JsonRPCVersion, "id": id, "result": json.RawMessage(raw),
	})
	fmt.Fprintf(w, "%s\n", line)
}

func TestHealth_openConn(t *testing.T) {
	conn, _, _ := makePipeConn(t)
	if err := conn.Health(context.Background()); err != nil {
		t.Errorf("expected healthy, got: %v", err)
	}
}

func TestHealth_closedConn(t *testing.T) {
	conn, _, _ := makePipeConn(t)
	conn.closeDone()
	if err := conn.Health(context.Background()); err == nil {
		t.Error("expected error for closed connection")
	}
}

func TestNewStdioConnection_hungUpstreamKilledOnDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	conn, err := NewStdioConnection(ctx, StdioCommand{
		Command: "sleep",
		Args:    []string{"30"},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	elapsed := time.Since(start)

	if err == nil {
		conn.Close()
		t.Fatal("expected handshake to fail for an upstream that never answers initialize")
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("handshake did not respect deadline, took %v", elapsed)
	}
}

func newWriteOnlyConn(stdin io.WriteCloser) *StdioConnection {
	return newConn(newStdioConnParams{
		cmd: new(exec.Cmd), stdin: stdin, stdout: io.NopCloser(strings.NewReader("")),
		logger: slog.New(slog.DiscardHandler),
	})
}

type discardWriteCloser struct{ io.Writer }

func (discardWriteCloser) Close() error { return nil }

type failingWriteCloser struct{}

func (failingWriteCloser) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
func (failingWriteCloser) Close() error              { return nil }

func TestStartSubprocessReportsAMissingCommand(t *testing.T) {
	_, err := startSubprocess(context.Background(), StdioCommand{
		Command: "mini-no-such-command",
		Logger:  slog.New(slog.DiscardHandler),
	})
	if err == nil || !strings.Contains(err.Error(), "start mini-no-such-command") {
		t.Fatalf("error = %v, want a start failure naming the command", err)
	}
}
