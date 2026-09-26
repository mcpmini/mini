//go:build test

package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

const responseCleanupTimer = 1

const statusNotRetriedByTransport = http.StatusInternalServerError

var pingTools = []map[string]any{
	{"name": "ping", "description": "ping", "inputSchema": map[string]any{"type": "object"}},
}

func upstreamFailingFirst(t *testing.T, failures int32, afterFailures http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) <= failures {
			http.Error(w, "server error", statusNotRetriedByTransport)
			return
		}
		afterFailures(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, &attempts
}

func serveMCP(tools []map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { fakeMCPHandle(w, r, tools) }
}

func requireBearer(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
}

type startupRetry struct {
	srv   *server.Server
	clock *clock.Fake
}

func startRetrying(t *testing.T, url string, logs slog.Handler) startupRetry {
	t.Helper()
	fakeClock := clock.NewFake()
	srv := newConnectTestServerLogging(t, logs, server.WithClock(fakeClock))
	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{{Name: "svc", Transport: "http", URL: url}})
	return startupRetry{srv: srv, clock: fakeClock}
}

func discardLogs() slog.Handler { return slog.NewTextHandler(io.Discard, nil) }

func (r startupRetry) waitForBackoffTimer(t *testing.T) {
	t.Helper()
	if err := r.clock.BlockUntilContext(t.Context(), responseCleanupTimer+1); err != nil {
		t.Fatalf("waiting for retry backoff timer: %v", err)
	}
}

type logRecorder chan slog.Record

func (h logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (h logRecorder) Handle(_ context.Context, r slog.Record) error {
	select {
	case h <- r:
	default:
	}
	return nil
}
func (h logRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h logRecorder) WithGroup(string) slog.Handler      { return h }

func (h logRecorder) next(t *testing.T, msg string) map[string]slog.Value {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case r := <-h:
			if r.Message == msg {
				return recordAttrs(r)
			}
		case <-deadline:
			t.Fatalf("%q not logged within 5s", msg)
			return nil
		}
	}
}

func recordAttrs(r slog.Record) map[string]slog.Value {
	attrs := map[string]slog.Value{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value
		return true
	})
	return attrs
}

func TestConnectUpstreamAsync_transientFailure_retriesAndRegisters(t *testing.T) {
	ts, _ := upstreamFailingFirst(t, 1, serveMCP(pingTools))
	r := startRetrying(t, ts.URL, discardLogs())
	defer r.srv.Close()

	r.waitForBackoffTimer(t)
	r.clock.Advance(time.Second)

	eventually(t, func() bool { return r.srv.ToolCount("svc") > 0 })
}

func TestConnectUpstreamAsync_repeatedFailure_backoffDoubles(t *testing.T) {
	ts, _ := upstreamFailingFirst(t, 2, serveMCP(pingTools))
	logs := make(logRecorder, 64)
	r := startRetrying(t, ts.URL, logs)
	defer r.srv.Close()

	logs.next(t, "upstream unavailable at startup, retrying")
	r.waitForBackoffTimer(t)
	r.clock.Advance(time.Second)

	if got := logs.next(t, "upstream unavailable at startup, retrying")["backoff"].Duration(); got != 2*time.Second {
		t.Errorf("second retry backoff = %v, want 2s", got)
	}
}

func TestConnectUpstreamAsync_reauthFailure_stopsRetrying(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(requireBearer))
	t.Cleanup(ts.Close)
	r := startRetrying(t, ts.URL, discardLogs())
	defer r.srv.Close()

	stopped := make(chan struct{})
	go func() { r.srv.WaitForStartupConnects(); close(stopped) }()
	retryScheduled := make(chan struct{})
	go func() {
		if r.clock.BlockUntilContext(t.Context(), responseCleanupTimer+1) == nil {
			close(retryScheduled)
		}
	}()

	select {
	case <-stopped:
	case <-retryScheduled:
		t.Fatal("scheduled a retry after a reauth error")
	}
}

func TestConnectUpstreamAsync_reauthAfterRetry_logsTheAuthError(t *testing.T) {
	ts, _ := upstreamFailingFirst(t, 1, requireBearer)
	logs := make(logRecorder, 64)
	r := startRetrying(t, ts.URL, logs)
	defer r.srv.Close()

	r.waitForBackoffTimer(t)
	r.clock.Advance(time.Second)

	if err := logs.next(t, "upstream needs authorization, not retrying")["err"]; err.String() == "" {
		t.Error("authorization warning should carry the upstream error")
	}
}

func TestConnectUpstreamAsync_closeDuringRetry_returnsPromptly(t *testing.T) {
	ts, _ := upstreamFailingFirst(t, 1, serveMCP(pingTools))
	r := startRetrying(t, ts.URL, discardLogs())

	r.waitForBackoffTimer(t)

	mustCloseWithin(t, r.srv, 3*time.Second)
}

func TestConnectUpstreamAsync_removedDuringRetry_isNotRedialed(t *testing.T) {
	ts, attempts := upstreamFailingFirst(t, 1, serveMCP(pingTools))
	r := startRetrying(t, ts.URL, discardLogs())
	defer mustCloseWithin(t, r.srv, 3*time.Second)

	r.waitForBackoffTimer(t)
	serve(t, r.srv, callTool("config", map[string]any{"action": "remove_server", "server": "svc"}))
	r.clock.Advance(time.Second)
	r.srv.WaitForStartupConnects()

	if got := attempts.Load(); got != 1 {
		t.Errorf("removed server was dialed %d times, want 1", got)
	}
	if r.srv.ToolCount("svc") != 0 {
		t.Error("svc should not be registered after being removed during startup retry")
	}
}

func TestConnectUpstreamAsync_runtimeAddDuringRetry_isNotOverwritten(t *testing.T) {
	ts, attempts := upstreamFailingFirst(t, 1, serveMCP(pingTools))
	r := startRetrying(t, ts.URL, discardLogs())
	defer mustCloseWithin(t, r.srv, 3*time.Second)

	r.waitForBackoffTimer(t)
	if err := r.srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fakeConn("runtime_a", "runtime_b")); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	r.clock.Advance(time.Second)
	r.srv.WaitForStartupConnects()

	if got := attempts.Load(); got != 1 {
		t.Errorf("startup upstream was redialed %d times after a runtime add, want 1", got)
	}
	if got := r.srv.ToolCount("svc"); got != 2 {
		t.Errorf("expected the 2 runtime tools, got %d (startup retry overwrote them)", got)
	}
}

func TestConnectUpstreams_removedBeforeFirstAttempt_isNotInstalled(t *testing.T) {
	ts := httptest.NewServer(serveMCP(pingTools))
	t.Cleanup(ts.Close)
	srv := newConnectTestServer(t)
	defer srv.Close()

	srv.ConnectUpstreams(context.Background(), []config.ServerConfig{{Name: "svc", Transport: "http", URL: ts.URL}})
	serve(t, srv, callTool("config", map[string]any{"action": "remove_server", "server": "svc"}))
	srv.WaitForStartupConnects()

	if srv.ToolCount("svc") != 0 {
		t.Error("svc was installed although it was removed before its startup connect ran")
	}
}
