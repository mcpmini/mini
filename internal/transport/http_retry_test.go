//go:build test

package transport

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

// These tests call rpc rather than Call because the fake servers return
// hardcoded JSON-RPC IDs; the initialize round-trip Call prepends would shift
// those IDs and corrupt per-server call counts.

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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("always rate limited"))
	}))
	defer srv.Close()

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
	_, err := conn.rpc(t.Context(), "ping", nil)
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

	clk := clock.NewFake()
	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clk})
	done := make(chan error, 1)
	go func() {
		_, err := conn.rpc(t.Context(), "ping", nil)
		done <- err
	}()
	advanceRetryTimer(t, clk)
	advanceRetryTimer(t, clk)
	if err := <-done; err != nil {
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("wait 60s"))
	}))
	defer srv.Close()

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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
	_, err := conn.rpc(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if calls.Load() != 1 {
		t.Errorf("401 should not be retried, got %d calls", calls.Load())
	}
}

type fakeAuthProvider struct {
	mu         sync.Mutex
	current    string
	next       string
	refreshes  int
	refreshErr error
	lastStale  string
}

func (f *fakeAuthProvider) Authorization(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current, nil
}

func (f *fakeAuthProvider) RefreshAuthorization(_ context.Context, stale string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastStale = stale
	if f.current != stale {
		return f.current, nil
	}
	f.refreshes++
	if f.refreshErr != nil {
		return "", f.refreshErr
	}
	f.current = f.next
	return f.current, nil
}

func (f *fakeAuthProvider) refreshCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshes
}

func (f *fakeAuthProvider) recordedStale() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastStale
}

func newAuthReplayConn(t *testing.T, handler http.HandlerFunc) (*HTTPConnection, *fakeAuthProvider) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	provider := &fakeAuthProvider{current: "Bearer old", next: "Bearer new"}
	conn, err := NewHTTPConnection(HTTPConnectionConfig{
		URL: srv.URL, Clock: clock.NewFake(), ServerName: "myserver", AuthProvider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, provider
}

func TestAuthReplay_401RefreshedThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write(okRPCResponse(1)) //nolint:errcheck
	})
	if _, err := conn.rpc(t.Context(), "ping", nil); err != nil {
		t.Fatalf("expected success after 401 refresh replay, got: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("upstream attempts = %d, want 2", calls.Load())
	}
	if provider.refreshCount() != 1 {
		t.Errorf("refreshes = %d, want 1", provider.refreshCount())
	}
}

func TestAuthReplay_persistent401_terminalAfterTwoAttempts(t *testing.T) {
	var calls atomic.Int32
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := conn.rpc(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected terminal error")
	}
	if !strings.Contains(err.Error(), "myserver requires re-authorization") || !strings.Contains(err.Error(), "mini auth myserver") {
		t.Errorf("terminal error should name server and remedy, got: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("upstream attempts = %d, want exactly 2", calls.Load())
	}
	if provider.refreshCount() != 1 {
		t.Errorf("refreshes = %d, want exactly 1", provider.refreshCount())
	}
}

func TestAuthReplay_refreshFailure_noReplay(t *testing.T) {
	var calls atomic.Int32
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	provider.refreshErr = fmt.Errorf("myserver requires re-authorization; run `mini auth myserver`: token endpoint down")
	_, err := conn.rpc(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "mini auth myserver") {
		t.Errorf("error should name remedy, got: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("upstream attempts = %d, want 1 (no replay after failed refresh)", calls.Load())
	}
}

func TestAuthReplay_429Then401_budgetNotMultiplied(t *testing.T) {
	var calls atomic.Int32
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1, 3:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.Write(okRPCResponse(1)) //nolint:errcheck
		}
	})
	if _, err := conn.rpc(t.Context(), "ping", nil); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if calls.Load() != 4 {
		t.Errorf("upstream attempts = %d, want 4 (429,401 then 429,200)", calls.Load())
	}
	if provider.refreshCount() != 1 {
		t.Errorf("refreshes = %d, want exactly 1", provider.refreshCount())
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

func TestAuthReplay_staleMatchesSentHeader(t *testing.T) {
	var capturedAuth string
	var captureOnce sync.Once
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		captureOnce.Do(func() { capturedAuth = r.Header.Get("Authorization") })
		if r.Header.Get("Authorization") == "Bearer old" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write(okRPCResponse(1)) //nolint:errcheck
	})
	if _, err := conn.rpc(t.Context(), "ping", nil); err != nil {
		t.Fatalf("expected success after replay, got: %v", err)
	}
	stale := provider.recordedStale()
	if capturedAuth != stale {
		t.Errorf("stale passed to RefreshAuthorization = %q, want %q (header server received on 401)", stale, capturedAuth)
	}
}
