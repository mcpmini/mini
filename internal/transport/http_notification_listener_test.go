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

	advanceListenerTimer(t, clk, time.Second)
	advanceListenerTimer(t, clk, 2*time.Second)

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
