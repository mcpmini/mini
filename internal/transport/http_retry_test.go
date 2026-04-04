package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newRateLimitedServer(t *testing.T, failUntilCall int32, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < failUntilCall {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("rate limited"))
			return
		}
		w.Write(okRPCResponse(1))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRetry_429WithRetryAfter_retriesAndSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := newRateLimitedServer(t, 3, &calls)
	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, NoVersionHeader: true})
	result, err := conn.Call(t.Context(), "ping", nil)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("always rate limited"))
	}))
	defer srv.Close()

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, NoVersionHeader: true})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention 429, got: %v", err)
	}
	if calls.Load() != maxRetries {
		t.Errorf("expected exactly %d calls, got %d", maxRetries, calls.Load())
	}
}

func TestRetry_503WithRetryAfter_retries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("down"))
			return
		}
		w.Write(okRPCResponse(1))
	}))
	defer srv.Close()

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, NoVersionHeader: true})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err != nil {
		t.Fatalf("expected success after 503 retry, got: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 calls, got %d", calls.Load())
	}
}

func TestRetry_429WithoutRetryAfter_usesExponentialBackoff(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("no retry header"))
			return
		}
		w.Write(okRPCResponse(1))
	}))
	defer srv.Close()

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, NoVersionHeader: true})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := conn.Call(ctx, "ping", nil)
	if err != nil {
		t.Fatalf("expected success after exponential backoff retries, got: %v", err)
	}
}

func TestRetry_contextCancelledDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("wait 60s"))
	}))
	defer srv.Close()

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, NoVersionHeader: true})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := conn.Call(ctx, "ping", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error when context canceled during backoff")
	}
	if elapsed > 5*time.Second {
		t.Errorf("took too long waiting for backoff: %v", elapsed)
	}
}

func TestRetry_nonRetryable4xx_noRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, NoVersionHeader: true})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if calls.Load() != 1 {
		t.Errorf("401 should not be retried, got %d calls", calls.Load())
	}
}

func TestRetry_passThroughRateLimits_returnsImmediately(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{
		URL:                   srv.URL,
		NoVersionHeader:       true,
		DisableRetryOnRateLimit: true,
	})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Errorf("pass-through mode should not retry, got %d calls", calls.Load())
	}
}
