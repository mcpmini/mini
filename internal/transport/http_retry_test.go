//go:build test

package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
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
	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
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

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
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

	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
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

	clk := clock.NewFake()
	conn, _ := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clk})
	done := make(chan error, 1)
	go func() {
		_, err := conn.Call(t.Context(), "ping", nil)
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

	_, err := conn.Call(ctx, "ping", nil)
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
	_, err := conn.Call(t.Context(), "ping", nil)
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
	authValues []string
	lastStale  string
	authCalls  int
}

func (f *fakeAuthProvider) Authorization(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authCalls++
	if len(f.authValues) > 0 {
		value := f.authValues[0]
		f.authValues = f.authValues[1:]
		return value, nil
	}
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

func (f *fakeAuthProvider) staleValue() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastStale
}

func (f *fakeAuthProvider) authorizationCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authCalls
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
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("expected success after 401 refresh replay, got: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("upstream attempts = %d, want 2", calls.Load())
	}
	if provider.refreshCount() != 1 {
		t.Errorf("refreshes = %d, want 1", provider.refreshCount())
	}
}

func TestAuthReplay_refreshUsesAuthorizationActuallySent(t *testing.T) {
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write(okRPCResponse(1)) //nolint:errcheck
	})
	provider.current = "Bearer new"
	provider.authValues = []string{"Bearer old-sent"}
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("expected success after replay, got: %v", err)
	}
	if got := provider.staleValue(); got != "Bearer old-sent" {
		t.Errorf("stale = %q, want the value actually sent in the first request", got)
	}
	if got := provider.authorizationCalls(); got != 2 {
		t.Errorf("Authorization calls = %d, want 2 (one per request)", got)
	}
}

func TestAuthReplay_persistent401_terminalAfterTwoAttempts(t *testing.T) {
	var calls atomic.Int32
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := conn.Call(t.Context(), "ping", nil)
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
	provider.refreshErr = errors.New("token endpoint down")
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "token endpoint down") {
		t.Errorf("error should contain cause, got: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("upstream attempts = %d, want 1 (no replay after failed refresh)", calls.Load())
	}
}

func TestAuthReplay_429Then401_budgetNotMultiplied(t *testing.T) {
	var calls atomic.Int32
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		if n < maxRetries {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		} else if n == maxRetries {
			w.WriteHeader(http.StatusUnauthorized)
		} else {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		}
	})
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error when second post exhausts 429 budget")
	}
	want := 2 * maxRetries
	if int(calls.Load()) != want {
		t.Errorf("upstream attempts = %d, want %d (2*maxRetries)", calls.Load(), want)
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
	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Errorf("pass-through mode should not retry, got %d calls", calls.Load())
	}
}
