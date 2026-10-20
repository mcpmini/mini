//go:build test

package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStdioWriteCancellationInterruptsABlockedPipeWrite(t *testing.T) {
	writer := &blockedDeadlineWriter{started: make(chan struct{}), interrupted: make(chan struct{})}
	conn := newWriteOnlyConn(writer)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- conn.writeJSONContext(ctx, Notification{Method: "test"}) }()
	<-writer.started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, errWriteInterrupted) {
			t.Fatalf("write error = %v, want interrupted write", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt a blocked write")
	}
}

func TestStdioWriteCancellationDropsAQueuedWrite(t *testing.T) {
	conn := newWriteOnlyConn(discardWriteCloser{Writer: io.Discard})
	if err := conn.lockWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer conn.unlockWrite()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- conn.writeJSONContext(ctx, Notification{Method: "test"}) }()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued write error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled queued write did not return")
	}
}

func TestStdioFailedWriteEndsTheConnection(t *testing.T) {
	conn := newWriteOnlyConn(failingWriteCloser{})
	if _, err := conn.Call(context.Background(), "ping", nil); err == nil {
		t.Fatal("Call succeeded through a failing stdin")
	}
	if conn.Health(context.Background()) == nil {
		t.Fatal("connection stayed usable after a write that may have left a partial line")
	}
}

var errWriteInterrupted = errors.New("write interrupted")

type blockedDeadlineWriter struct {
	started     chan struct{}
	interrupted chan struct{}
	once        sync.Once
}

func (w *blockedDeadlineWriter) Close() error { return nil }

func (w *blockedDeadlineWriter) Write([]byte) (int, error) {
	close(w.started)
	<-w.interrupted
	return 0, errWriteInterrupted
}

func (w *blockedDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		w.once.Do(func() { close(w.interrupted) })
	}
	return nil
}

func newWriteOnlyConn(stdin io.WriteCloser) *StdioConnection {
	return newConn(new(exec.Cmd), stdin, io.NopCloser(strings.NewReader("")), slog.New(slog.DiscardHandler))
}

type discardWriteCloser struct{ io.Writer }

func (discardWriteCloser) Close() error { return nil }

type failingWriteCloser struct{}

func (failingWriteCloser) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
func (failingWriteCloser) Close() error              { return nil }
