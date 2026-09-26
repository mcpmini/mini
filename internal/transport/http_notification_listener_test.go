//go:build test

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func newNotifListenerServer(t *testing.T, getHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			getHandler(w, r)
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		switch req["method"] {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{
					"protocolVersion": ProtocolVersion,
					"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		default:
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"ok": true},
			})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func advanceListenerTimer(t *testing.T, clk *clock.Fake, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("waiting for listener timer: %v", err)
	}
	clk.Advance(d)
}

func advanceListenerTimerAndAwaitNextSleep(t *testing.T, clk *clock.Fake, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("waiting for pre-advance listener timer: %v", err)
	}
	clk.Advance(d)
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("waiting for post-advance listener timer: %v", err)
	}
}

func TestNotificationListener_backoffDoublesOnPersistentFailure(t *testing.T) {
	provider := &fakeAuthProvider{current: "Bearer tok", refreshErr: errors.New("token refresh failed")}
	clk := clock.NewFake()

	srv := newNotifListenerServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	conn, err := NewHTTPConnection(HTTPConnectionConfig{
		URL: srv.URL, Clock: clk, AuthProvider: provider, AuthHeaderName: "Authorization",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("handshake failed: %v", err)
	}

	advanceListenerTimerAndAwaitNextSleep(t, clk, time.Second)
	if got := provider.refreshCount(); got != 2 {
		t.Errorf("after 1s: refreshes = %d, want 2 (initial + first retry)", got)
	}

	clk.Advance(time.Second)
	advanceListenerTimerAndAwaitNextSleep(t, clk, time.Second)
	if got := provider.refreshCount(); got != 3 {
		t.Errorf("after 1s+1s: refreshes = %d, want 3 (the doubled 2s backoff must not retry at 1s)", got)
	}
}

type failNRefreshProvider struct {
	mu        sync.Mutex
	failsLeft int
	current   string
	next      string
}

func (p *failNRefreshProvider) Authorization(_ context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current, nil
}

func (p *failNRefreshProvider) RefreshAuthorization(_ context.Context, _ string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failsLeft > 0 {
		p.failsLeft--
		return "", fmt.Errorf("refresh failed: %w", ErrReauthRequired)
	}
	p.current = p.next
	return p.current, nil
}

func TestNotificationListener_survivesReauthRequired(t *testing.T) {
	streamOpened := make(chan struct{}, 1)
	srv := newNotifListenerServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"%s\"}\n\n", NotificationToolsChanged) //nolint:errcheck
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		streamOpened <- struct{}{}
		<-r.Context().Done()
	})

	provider := &failNRefreshProvider{failsLeft: 2, current: "Bearer tok", next: "Bearer new"}
	clk := clock.NewFake()
	conn, err := NewHTTPConnection(HTTPConnectionConfig{
		URL: srv.URL, Clock: clk, AuthProvider: provider, AuthHeaderName: "Authorization",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("handshake failed: %v", err)
	}

	advanceListenerTimer(t, clk, maxListenerBackoff)
	advanceListenerTimer(t, clk, maxListenerBackoff)

	select {
	case <-streamOpened:
	case <-time.After(5 * time.Second):
		t.Fatal("notification stream never opened; listener stopped on ErrReauthRequired")
	}
}

func TestNotificationListener_successResetsBackoffToOne(t *testing.T) {
	var getCount atomic.Int32
	get4Fired := make(chan struct{})
	var closeOnce sync.Once
	clk := clock.NewFake()

	srv := newNotifListenerServer(t, func(w http.ResponseWriter, r *http.Request) {
		n := getCount.Add(1)
		switch n {
		case 1, 2:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 3:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
		default:
			closeOnce.Do(func() { close(get4Fired) })
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-r.Context().Done()
		}
	})

	conn, err := NewHTTPConnection(HTTPConnectionConfig{URL: srv.URL, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("handshake failed: %v", err)
	}

	advanceListenerTimerAndAwaitNextSleep(t, clk, time.Second)
	advanceListenerTimerAndAwaitNextSleep(t, clk, 2*time.Second)
	// GET 4 hangs, so no next timer registers; the non-awaiting helper suffices.
	advanceListenerTimer(t, clk, time.Second)
	select {
	case <-get4Fired:
	case <-time.After(3 * time.Second):
		t.Error("listener did not reconnect after 1s; 200 did not reset backoff")
	}
}

func TestListenerDelay_failuresDoubleToCapAndSuccessResets(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		backoff time.Duration
		reauth  bool
		delay   time.Duration
		next    time.Duration
	}{
		{"first failure", 503, time.Second, false, time.Second, 2 * time.Second},
		{"doubling", 503, 2 * time.Second, false, 2 * time.Second, 4 * time.Second},
		{"cap at 60s from 40s", 503, 40 * time.Second, false, 40 * time.Second, 60 * time.Second},
		{"stays at 60s cap", 503, 60 * time.Second, false, 60 * time.Second, 60 * time.Second},
		{"reset after 200", 200, 30 * time.Second, false, time.Second, time.Second},
		{"reauth uses max backoff", 0, time.Second, true, maxListenerBackoff, maxListenerBackoff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			delay, next := listenerDelay(tc.status, tc.backoff, tc.reauth)
			if delay != tc.delay {
				t.Errorf("delay = %v, want %v", delay, tc.delay)
			}
			if next != tc.next {
				t.Errorf("next = %v, want %v", next, tc.next)
			}
		})
	}
}

func TestNotificationListener_survivesClientTimeout(t *testing.T) {
	const clientTimeout = 50 * time.Millisecond
	const delayBeyondClientTimeout = 5 * clientTimeout
	notifReceived := make(chan struct{}, 1)

	srv := newNotifListenerServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-time.After(delayBeyondClientTimeout):
		case <-r.Context().Done():
			return
		}
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"%s\"}\n\n", NotificationToolsChanged) //nolint:errcheck
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})

	clk := clock.NewFake()
	conn, err := NewHTTPConnection(HTTPConnectionConfig{
		URL:           srv.URL,
		Clock:         clk,
		ClientTimeout: clientTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	conn.SetNotificationHandler(func(n Notification) {
		if n.Method == NotificationToolsChanged {
			select {
			case notifReceived <- struct{}{}:
			default:
			}
		}
	})
	t.Cleanup(func() { conn.Close() })

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("handshake failed: %v", err)
	}

	select {
	case <-notifReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("notification not received; stream may have been killed by the regular client timeout")
	}
}

func TestNotificationListener_reauthWaitsMaxListenerBackoff(t *testing.T) {
	provider := &fakeAuthProvider{current: "Bearer old", next: "Bearer new"}
	clk := clock.NewFake()

	srv := newNotifListenerServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	conn, err := NewHTTPConnection(HTTPConnectionConfig{
		URL: srv.URL, Clock: clk, AuthProvider: provider, AuthHeaderName: "Authorization",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	conn.Call(t.Context(), "ping", nil) //nolint:errcheck

	// First pass: 401 → refresh(1) → replay 401 → reauth, waits maxListenerBackoff.
	// Advance half of maxListenerBackoff; timer must not have fired.
	advanceListenerTimer(t, clk, maxListenerBackoff/2)
	if got := provider.refreshCount(); got != 1 {
		t.Errorf("after half maxListenerBackoff: refreshes = %d, want 1 (no retry yet)", got)
	}

	// Advance the remaining half; timer fires and second pass runs.
	advanceListenerTimerAndAwaitNextSleep(t, clk, maxListenerBackoff/2)
	if got := provider.refreshCount(); got != 2 {
		t.Errorf("after full maxListenerBackoff: refreshes = %d, want 2", got)
	}
}

func TestNewStreamClient_blockPrivateIPs_sharesSSRFTransport(t *testing.T) {
	conn, err := NewHTTPConnection(HTTPConnectionConfig{
		URL:             "http://example.com",
		Clock:           clock.NewFake(),
		BlockPrivateIPs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if conn.newStreamClient().Transport != conn.client.Transport {
		t.Error("stream client must share Transport with regular client to preserve SSRF protection")
	}
}

func TestNewStreamClient_redirect_notFollowed(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
	}))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)
	conn, err := NewHTTPConnection(HTTPConnectionConfig{URL: redirector.URL, Clock: clock.NewFake()})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := conn.newStreamClient().Get(redirector.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || targetHits.Load() != 0 {
		t.Errorf("status = %d, redirect target hits = %d: the stream client must not follow redirects", resp.StatusCode, targetHits.Load())
	}
}
