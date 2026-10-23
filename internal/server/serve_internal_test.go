//go:build test

package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

func TestSerializedWriterKeepsFirstErrorAcrossResponsesAndNotifications(t *testing.T) {
	want := errors.New("output failed")
	var writes atomic.Int32
	out := writerFunc(func([]byte) (int, error) {
		writes.Add(1)
		return 0, want
	})
	w := newSerializedWriter(out, func() {})
	w.Write(map[string]any{"id": 1})
	w.Write(json.RawMessage(`{"method":"notifications/tools/list_changed"}`))
	w.Write(map[string]any{"id": 2})
	if !errors.Is(w.Err(), want) {
		t.Fatalf("latched error = %v, want failure cause %v", w.Err(), want)
	}
	if got := writes.Load(); got != 1 {
		t.Fatalf("writes = %d, want 1", got)
	}
}

func TestSerializedWriterReportsEncodingFailureWithoutWrite(t *testing.T) {
	wantCancel := make(chan struct{})
	wantObserve := make(chan error, 1)
	w := newSerializedWriter(writerFunc(func([]byte) (int, error) {
		t.Fatal("encoding failure should not call Write")
		return 0, nil
	}), func() { close(wantCancel) })
	w.onFailure = func(err error) { wantObserve <- err }
	w.Write(make(chan int))
	err := <-wantObserve
	var unsupported *json.UnsupportedTypeError
	if !errors.As(err, &unsupported) {
		t.Fatalf("observer error = %v, want JSON encoding error", err)
	}
	<-wantCancel
}

func TestWriteNotificationsReturnsBeforeFlushOnWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := make(chan json.RawMessage, 1)
	stream <- json.RawMessage(`{"method":"notifications/tools/list_changed"}`)
	flusher := &countingFlusher{}
	w := failingHTTPWriter{}
	srv := &Server{}
	r := httptest.NewRequest(http.MethodGet, "/mcp", nil).WithContext(ctx)
	srv.writeNotifications(notificationParams{
		Request: r, Writer: w, Flusher: flusher, Stream: stream,
	})
	if ctx.Err() != nil {
		t.Fatalf("request context canceled: %v", ctx.Err())
	}
	if got := flusher.calls.Load(); got != 0 {
		t.Fatalf("flushes = %d, want 0 after failed write", got)
	}
	select {
	case stream <- json.RawMessage(`{"method":"still-open"}`):
	default:
		t.Fatal("notification stream was closed or full after write failure")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

type failingHTTPWriter struct{}

func (failingHTTPWriter) Header() http.Header       { return make(http.Header) }
func (failingHTTPWriter) WriteHeader(int)           {}
func (failingHTTPWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type countingFlusher struct{ calls atomic.Int32 }

func (f *countingFlusher) Flush() { f.calls.Add(1) }

func TestServeReturnsPendingNotificationFailureAtEOF(t *testing.T) {
	session := newSession(clock.System())
	session.toolsChanged.publishToolsChanged()
	want := errors.New("notification output failed")
	out := writerFunc(func([]byte) (int, error) { return 0, want })
	err := (&Server{}).serveLoop(context.Background(), serveLoopParams{
		Input: strings.NewReader(""), Output: out, Session: session,
	})
	if !errors.Is(err, want) {
		t.Fatalf("Serve error = %v, want pending notification failure %v", err, want)
	}
}

func TestPeekRequestID(t *testing.T) {
	tests := []struct{ line, want string }{
		{`{"id":7,"method":"ping"}`, `7`},
		{`{"id":"7","method":"ping"}`, `"7"`},
		{`{"id":null,"method":"ping"}`, ``},
		{`{"method":"notifications/initialized"}`, ``},
		{`not json`, ``},
	}
	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			if got := peekRequestID([]byte(tc.line)); string(got) != tc.want {
				t.Fatalf("peekRequestID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHandleLineRejectsBadEnvelopes(t *testing.T) {
	srv := newRouteTestServer(transport.ToolModeCompact)
	tests := []struct {
		name, line string
		id         any
		code       int
	}{
		{name: "malformed JSON", line: `{"jsonrpc":`, id: nil, code: transport.CodeParseError},
		{
			name: "wrong jsonrpc version",
			line: `{"jsonrpc":"1.0","id":"a","method":"ping"}`,
			id:   "a",
			code: transport.CodeInvalidRequest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, send := srv.handleLine(context.Background(), []byte(tc.line), newSession(clock.NewFake()))
			if !send || resp.ID != tc.id || resp.Error == nil || resp.Error.Code != tc.code {
				t.Fatalf("response = %+v (send=%v), want id %v code %d", resp, send, tc.id, tc.code)
			}
		})
	}
}

func TestHandleLineRoutesNotificationsWithoutReplying(t *testing.T) {
	session := newSession(clock.NewFake())
	session.markInitialized()
	line := []byte(`{"jsonrpc":"2.0","method":"` + transport.NotificationInitialized + `"}`)
	if _, send := newRouteTestServer(transport.ToolModeCompact).handleLine(context.Background(), line, session); send {
		t.Fatal("a notification produced a response")
	}
	if !session.clientReady.Load() {
		t.Fatal("notifications/initialized was not routed")
	}
}

func TestHandleScannedLineAppliesCancellationBeforeReturning(t *testing.T) {
	session := newSession(clock.NewFake())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session.registerInFlight(json.RawMessage(`1`), cancel)
	var wg sync.WaitGroup
	newRouteTestServer(transport.ToolModeCompact).handleScannedLine(handleScannedLineParams{
		ctx:      context.Background(),
		rawLine:  []byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`),
		session:  session,
		writeOut: func(any) {},
		wg:       &wg,
	})
	if ctx.Err() == nil {
		t.Fatal("cancellation was still pending when the next line could be read")
	}
	wg.Wait()
}

func TestServeLoopAbortsInitializationWaitersAtEOF(t *testing.T) {
	session := newSession(clock.NewFake())
	err := newRouteTestServer(transport.ToolModeCompact).serveLoop(context.Background(), serveLoopParams{
		Input: strings.NewReader(""), Output: io.Discard, Session: session,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.initAbort:
	default:
		t.Fatal("requests waiting on initialize were not released when input ended")
	}
}

func TestServeReturnsInputReadError(t *testing.T) {
	want := errors.New("stdin broke")
	reader := io.MultiReader(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), errReader{want})
	err := newRouteTestServer(transport.ToolModeCompact).Serve(context.Background(), reader, io.Discard)
	if !errors.Is(err, want) {
		t.Fatalf("Serve error = %v, want the input failure %v", err, want)
	}
}

func TestServeForgetsItsSessionOnReturn(t *testing.T) {
	srv := newRouteTestServer(transport.ToolModeCompact)
	if err := srv.Serve(context.Background(), strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := srv.sessions.count(); got != 0 {
		t.Fatalf("sessions after Serve returned = %d, want 0", got)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestWriteJSONReportsShortWrite(t *testing.T) {
	short := writerFunc(func(p []byte) (int, error) { return len(p) - 1, nil })
	if err := writeJSON(short, map[string]int{"id": 1}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("writeJSON error = %v, want io.ErrShortWrite", err)
	}
}

func TestSerializedWriterReportsWriteFailureToObservingOutput(t *testing.T) {
	want := errors.New("client went away")
	out := &failureObservingOutput{err: want}
	var cancelled atomic.Bool
	w := newSerializedWriter(out, func() { cancelled.Store(true) })
	w.observeResponseFailure(out)
	w.Write(map[string]int{"id": 1})
	if !errors.Is(out.observed, want) || !cancelled.Load() {
		t.Fatalf("observed = %v, cancelled = %v; want the write failure reported and serving cancelled",
			out.observed, cancelled.Load())
	}
}

type failureObservingOutput struct {
	err      error
	observed error
}

func (o *failureObservingOutput) Write([]byte) (int, error) { return 0, o.err }
func (o *failureObservingOutput) ResponseFailed(err error)  { o.observed = err }
