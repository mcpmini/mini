//go:build test

package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestAwaitResponse_ctxCancel(t *testing.T) {
	conn, _, _ := makePipeConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	ch := conn.pending.register(int64(10))
	cancel()

	_, err := conn.awaitResponse(ctx, 10, ch)
	if err == nil {
		t.Error("expected error on ctx cancel")
	}
}

func TestAwaitResponse_connClose(t *testing.T) {
	conn, _, _ := makePipeConn(t)
	ch := conn.pending.register(int64(11))
	conn.closeDone()

	_, err := conn.awaitResponse(context.Background(), 11, ch)
	if err == nil {
		t.Error("expected error when connection closed")
	}
}

func serveOneRequest(w io.Writer, r io.Reader, result any) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := NewScanner(r)
		if !scanner.Scan() {
			return
		}
		var req Request
		json.Unmarshal(scanner.Bytes(), &req)
		sendResponse(w, req.ID, result)
	}()
	return done
}

func TestCall_roundTrip(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	done := serveOneRequest(serverW, serverR, map[string]any{"value": "ok"})

	result, err := conn.Call(context.Background(), "tools/call", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got map[string]any
	json.Unmarshal(result, &got)
	if got["value"] != "ok" {
		t.Errorf("unexpected result: %v", got)
	}
	<-done
}

func TestCall_ctxCancelBeforeResponse(t *testing.T) {
	conn, _, serverR := makePipeConn(t)
	go io.Copy(io.Discard, serverR)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := conn.Call(ctx, "tools/call", nil)
	if err == nil {
		t.Error("expected error for canceled context")
	}
}

func TestCall_sendsCorrectJSONRPC(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := NewScanner(serverR)
		if !scanner.Scan() {
			return
		}
		var req Request
		json.Unmarshal(scanner.Bytes(), &req)

		sendResponse(serverW, req.ID, nil)
	}()

	conn.Call(context.Background(), "ping", nil)
	<-done
}

func TestListTools_viaPipe(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)

	go func() {
		scanner := NewScanner(serverR)
		if !scanner.Scan() {
			return
		}
		var req Request
		json.Unmarshal(scanner.Bytes(), &req)
		sendResponse(serverW, req.ID, map[string]any{
			"tools": []any{
				map[string]any{"name": "ping", "description": "ping tool", "inputSchema": map[string]any{}},
			},
		})
	}()
	toolList, err := conn.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(toolList.Tools) != 1 || toolList.Tools[0].Name != "ping" {
		t.Errorf("unexpected tools: %+v", toolList.Tools)
	}
}

func TestListTools_viaPipe_pagination(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)

	pages := []map[string]any{
		{"tools": []any{map[string]any{"name": "a", "inputSchema": map[string]any{}}}, "nextCursor": "p2"},
		{"tools": []any{map[string]any{"name": "b", "inputSchema": map[string]any{}}}},
	}
	cursorCh := make(chan string, len(pages))
	go func() {
		scanner := NewScanner(serverR)
		for _, page := range pages {
			if !scanner.Scan() {
				return
			}
			var req Request
			json.Unmarshal(scanner.Bytes(), &req) //nolint:errcheck
			var p struct {
				Cursor string `json:"cursor"`
			}
			json.Unmarshal(req.Params, &p) //nolint:errcheck
			cursorCh <- p.Cursor
			sendResponse(serverW, req.ID, page)
		}
	}()

	got, err := conn.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b"}; !slices.Equal(toolNames(got.Tools), want) {
		t.Errorf("got %v, want %v", toolNames(got.Tools), want)
	}
	gotCursors := []string{<-cursorCh, <-cursorCh}
	if want := []string{"", "p2"}; !slices.Equal(gotCursors, want) {
		t.Errorf("cursors: got %v, want %v", gotCursors, want)
	}
}

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
