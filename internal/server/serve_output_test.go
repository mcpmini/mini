package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
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
	ch := make(chan json.RawMessage, 1)
	ch <- json.RawMessage(`{"method":"notifications/tools/list_changed"}`)
	wait := startNotifyForwarder(w.Write, ch)
	close(ch)
	wait()
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
	srv.writeNotifications(r, w, flusher, stream)
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
	err := (&Server{}).serveLoop(context.Background(), strings.NewReader(""), out, session)
	if !errors.Is(err, want) {
		t.Fatalf("Serve error = %v, want pending notification failure %v", err, want)
	}
}
