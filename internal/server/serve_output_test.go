package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestToolChangeDuringListenAcknowledgementReachesTheListen(t *testing.T) {
	session := newSession(clock.System())
	session.modern.Store(true)
	var log messageLog
	writeAck, forwarded := forwardToolChangeDuringAck(session, log.record)
	listen := listenSubscription{
		id:            json.RawMessage(`7`),
		notifications: transport.ListenNotifications{ToolsListChanged: true},
	}
	session.acknowledgeListen(listen, func() {}, writeAck)
	<-forwarded
	written := log.messages()
	if len(written) != 2 {
		t.Fatalf("written = %+v, want the acknowledgement then one tagged tool change", written)
	}
	if _, tagged := written[1].(transport.ToolsListChangedNotification); !tagged {
		t.Fatalf("second message = %+v, want a tagged tool change", written[1])
	}
}

type messageLog struct {
	mu      sync.Mutex
	written []any
}

func (l *messageLog) record(message any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.written = append(l.written, message)
}

func (l *messageLog) messages() []any {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.written)
}

// forwardToolChangeDuringAck returns an acknowledgement writer that records the acknowledgement and
// then forwards a tool change concurrently, the race a listen must not lose.
func forwardToolChangeDuringAck(session *Session, record func(any)) (func(any), <-chan struct{}) {
	forwarded := make(chan struct{})
	return func(message any) {
		record(message)
		go func() {
			session.forwardToolsChanged(json.RawMessage(`{}`), record)
			close(forwarded)
		}()
		// Give a forwarder that is not held back by the acknowledgement time to run first.
		select {
		case <-forwarded:
		case <-time.After(50 * time.Millisecond):
		}
	}, forwarded
}
