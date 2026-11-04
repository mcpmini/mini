//go:build test

package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/testutil"
)

func newRateLimitedServer(t *testing.T, failUntilCall int32, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < failUntilCall {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("rate limited"))
			return
		}
		w.Write(okRPCResponse(1))
	})
	return srv
}

func TestRetry_429WithRetryAfter_retriesAndSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := newRateLimitedServer(t, 3, &calls)
	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
	result, err := conn.rpc(t.Context(), "ping", nil)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if result == nil {
		t.Error("expected non-nil result")
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 calls (2 retries), got %d", calls.Load())
	}
}

func TestRetry_429_exhaustsMaxRetries(t *testing.T) {
	var calls atomic.Int32
	srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("always rate limited"))
	})

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
	_, err := conn.rpc(t.Context(), "ping", nil)
	testutil.ErrorContains(t, err, "429")
	if calls.Load() != maxRetries {
		t.Errorf("expected exactly %d calls, got %d", maxRetries, calls.Load())
	}
}

func TestRetry_503WithRetryAfter_retries(t *testing.T) {
	var calls atomic.Int32
	srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("down"))
			return
		}
		w.Write(okRPCResponse(1))
	})

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
	_, err := conn.rpc(t.Context(), "ping", nil)
	if err != nil {
		t.Fatalf("expected success after 503 retry, got: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 calls, got %d", calls.Load())
	}
}

func TestRetry_429WithoutRetryAfter_usesExponentialBackoff(t *testing.T) {
	var calls atomic.Int32
	srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("no retry header"))
			return
		}
		w.Write(okRPCResponse(1))
	})

	clk := clock.NewFake()
	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clk})
	done := testutil.RunInBackground(func() error {
		_, err := conn.rpc(t.Context(), "ping", nil)
		return err
	})
	advanceRetryTimer(t, clk)
	advanceRetryTimer(t, clk)
	if err := testutil.WaitForReceive(t, done, "the retried rpc"); err != nil {
		t.Fatalf("expected success after exponential backoff retries, got: %v", err)
	}
}

func advanceRetryTimer(t *testing.T, clk *clock.Fake) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
}

func TestRetry_contextCancelledDuringBackoff(t *testing.T) {
	srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("wait 60s"))
	})

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, err := conn.rpc(ctx, "ping", nil)
	if err == nil {
		t.Fatal("expected error when context canceled during backoff")
	}
}

func TestRetry_nonRetryable4xx_noRetry(t *testing.T) {
	var calls atomic.Int32
	srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	})

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
	_, err := conn.rpc(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if calls.Load() != 1 {
		t.Errorf("401 should not be retried, got %d calls", calls.Load())
	}
}

func TestRetry_passThroughRateLimits_returnsImmediately(t *testing.T) {
	var calls atomic.Int32
	srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	})

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{
		URL:                     srv.URL,
		Clock:                   clock.NewFake(),
		DisableRetryOnRateLimit: true,
	})
	_, err := conn.rpc(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Errorf("pass-through mode should not retry, got %d calls", calls.Load())
	}
}
