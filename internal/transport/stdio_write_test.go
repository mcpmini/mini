//go:build test

package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
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

func TestStdioQueuedCallThatGivesUpKeepsTheConnection(t *testing.T) {
	conn := newWriteOnlyConn(discardWriteCloser{Writer: io.Discard})
	if err := conn.lockWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer conn.unlockWrite()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := conn.Call(ctx, "ping", nil); result <- err }()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued Call error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("a cancelled Call stayed queued behind another write")
	}
	if conn.Health(context.Background()) != nil {
		t.Fatal("giving up while queued ended the connection")
	}
}

func TestStdioWriteFinishingAsItsCallerGivesUpResetsTheDeadline(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resetErr error
		wantErr  bool
	}{
		{name: "reset succeeds"},
		{name: "reset fails", resetErr: errors.New("pipe closed"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			pipe := &recordingDeadlinePipe{onWrite: cancel, resetErr: tc.resetErr}
			err := newWriteOnlyConn(pipe).writeJSONContext(ctx, Notification{Method: "test"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("write error = %v, want error: %v", err, tc.wantErr)
			}
			if !tc.wantErr && !pipe.lastDeadline().IsZero() {
				t.Fatalf("stdin kept the interrupt deadline %v, so every later write would fail", pipe.lastDeadline())
			}
		})
	}
}

func TestStdioBlockedWriteOnAPipeWithoutDeadlinesEndsTheConnection(t *testing.T) {
	pipe := &noDeadlinePipe{started: make(chan struct{}), closed: make(chan struct{})}
	conn := newWriteOnlyConn(pipe)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := conn.Call(ctx, "ping", nil); result <- err }()
	<-pipe.started
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Call succeeded through a write that never finished")
		}
	case <-time.After(time.Second):
		t.Fatal("the write stayed blocked after its caller gave up")
	}
	if conn.Health(context.Background()) == nil {
		t.Fatal("connection stayed usable after an unfinished write")
	}
}

func TestStdioCancelledCallKeepsTheConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn := newWriteOnlyConn(failingWriteCloser{})
	// lockWrite picks between a free gate and the done context at random, so try both outcomes.
	for range 50 {
		if _, err := conn.Call(ctx, "ping", nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("Call error = %v, want context.Canceled before any write", err)
		}
	}
	if conn.Health(context.Background()) != nil {
		t.Fatal("a call cancelled before its write ended the connection")
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

type recordingDeadlinePipe struct {
	onWrite   func()
	resetErr  error
	mu        sync.Mutex
	deadlines []time.Time
}

func (p *recordingDeadlinePipe) Write(b []byte) (int, error) {
	p.onWrite()
	return len(b), nil
}

func (p *recordingDeadlinePipe) Close() error { return nil }

func (p *recordingDeadlinePipe) SetWriteDeadline(deadline time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deadlines = append(p.deadlines, deadline)
	if deadline.IsZero() {
		return p.resetErr
	}
	return nil
}

func (p *recordingDeadlinePipe) lastDeadline() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.deadlines[len(p.deadlines)-1]
}

type noDeadlinePipe struct {
	started   chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

func (p *noDeadlinePipe) Write([]byte) (int, error) {
	close(p.started)
	<-p.closed
	return 0, errors.New("write on closed pipe")
}

func (p *noDeadlinePipe) Close() error {
	p.closeOnce.Do(func() { close(p.closed) })
	return nil
}

func (p *noDeadlinePipe) SetWriteDeadline(time.Time) error {
	return errors.New("deadlines not supported")
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

const modernContinuationCall = `{"name":"confirm","arguments":{},"inputResponses":{"r1":{"accepted":true}},` +
	`"requestState":9007199254740993,"custom":1,` +
	`"_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{"form":{}}},"unknown":true}}`

func TestStdioModernCallKeepsContinuationFieldsAndMetadata(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	conn.setModern(ModernVersions[0])
	seen := scriptStdioUpstream(serverW, serverR, replyResult(serverW, json.RawMessage(`{"ok":true}`)))
	if _, err := conn.Call(context.Background(), "tools/call", json.RawMessage(modernContinuationCall)); err != nil {
		t.Fatalf("Call: %v", err)
	}
	call := <-seen
	assertRequestMeta(t, requestMetaParams{
		Params:          call.Params,
		ProtocolVersion: ModernVersions[0],
		Elicitation:     true,
	})
	kept := []string{
		`"inputResponses":{"r1":{"accepted":true}}`,
		`"requestState":9007199254740993`,
		`"custom":1`,
		`"unknown":true`,
	}
	for _, raw := range kept {
		if !strings.Contains(string(call.Params), raw) {
			t.Errorf("modern call params %s do not contain %s", call.Params, raw)
		}
	}
}

func TestStdioModernCancellationTargetsSentRequest(t *testing.T) {
	conn, _, serverR := makePipeConn(t)
	conn.setModern(ModernVersions[0])
	scanner := bufio.NewScanner(serverR)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := conn.Call(ctx, "tools/call", json.RawMessage(`{"name":"slow","arguments":{}}`))
		result <- err
	}()
	call := readScannerRequest(t, scanner)
	cancel()
	notice := readScannerRequest(t, scanner)
	var cancelled CancelledParams
	if notice.Method != NotificationCancelled || json.Unmarshal(notice.Params, &cancelled) != nil ||
		!sameJSONID(cancelled.RequestID, call.ID) {
		t.Fatalf(
			"after cancelling call %v mini sent %q %s, want a cancel for that call",
			call.ID,
			notice.Method,
			notice.Params,
		)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Call error = %v, want context.Canceled", err)
	}
}

func TestStdioCancellationGivesUpOnItsTurnToWrite(t *testing.T) {
	conn := newWriteOnlyConn(discardWriteCloser{Writer: io.Discard})
	fakeClock := clock.NewFake()
	conn.clock = fakeClock
	if err := conn.lockWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer conn.unlockWrite()
	sent := make(chan struct{})
	go func() {
		conn.cancelUpstreamRequest(context.Background(), 1)
		close(sent)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fakeClock.BlockUntilContext(ctx, 1); err != nil {
		t.Fatal(err)
	}
	fakeClock.Advance(stdioCancelWriteTimeout)
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("the cancellation kept waiting behind another write")
	}
	if conn.Health(context.Background()) != nil {
		t.Fatal("a cancellation that never started writing ended the connection")
	}
}
