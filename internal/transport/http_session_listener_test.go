//go:build test

package transport

import (
	"context"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestHTTPSession_get404NeverTriggersReinit(t *testing.T) {
	m, srv := newSessionServer(t)
	m.mu.Lock()
	m.alwaysNotFoundOnGET = true
	m.mu.Unlock()

	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	for range 5 {
		if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
			t.Fatalf("call failed: %v", err)
		}
	}

	if got := m.initializeCount(); got != 1 {
		t.Errorf("initialize count = %d, want 1 (GET 404 must not trigger re-init)", got)
	}

	m.mu.Lock()
	issuedSID := m.currentSID
	m.mu.Unlock()
	pings := m.requestsWithMethod("ping")
	for _, p := range pings {
		if p.sessionID != issuedSID {
			t.Errorf("ping carried session %q, want %q", p.sessionID, issuedSID)
		}
	}
}

func TestHTTPSession_listenerBackoffResetsAfterReinit(t *testing.T) {
	m, srv := newSessionServer(t)
	m.mu.Lock()
	m.alwaysNotFoundOnGET = true
	m.getSeen = make(chan string, 32)
	m.mu.Unlock()

	clk := clock.NewFake()
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clk})

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}

	drainGet := func() {
		t.Helper()
		select {
		case <-m.getSeen:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for GET")
		}
	}

	drainGet() // initial GET

	advanceListenerTimerAndAwaitNextSleep(t, clk, time.Second)
	drainGet()
	advanceListenerTimerAndAwaitNextSleep(t, clk, 2*time.Second)
	drainGet()

	m.mu.Lock()
	sid1 := m.currentSID
	m.mu.Unlock()
	m.expireSession(sid1)
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("re-init call failed: %v", err)
	}

	advanceListenerTimerAndAwaitNextSleep(t, clk, 4*time.Second)
	drainGet()

	clk.Advance(time.Second)
	select {
	case sid := <-m.getSeen:
		m.mu.Lock()
		currentSID := m.currentSID
		m.mu.Unlock()
		if sid != currentSID {
			t.Errorf("listener reconnected with session %q, want %q", sid, currentSID)
		}
	case <-time.After(3 * time.Second):
		t.Error("listener did not reconnect after 1s advance; backoff was not reset after re-init")
	}
}

func TestHTTPSession_reinitKeepsASingleNotificationListener(t *testing.T) {
	m, srv := newSessionServer(t)
	m.mu.Lock()
	m.getSeen = make(chan string, 16)
	m.notifInitialized = make(chan struct{}, 4)
	m.mu.Unlock()
	clk := clock.NewFake()
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clk})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}

	waitNotif := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		select {
		case <-m.notifInitialized:
		case <-ctx.Done():
			t.Fatal("notifications/initialized not received")
		}
	}
	waitNotif()
	firstSID := <-m.getSeen
	waitUntilListenerParkedOnClock(t, clk)

	m.expireSession(firstSID)
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("call after session expiry: %v", err)
	}
	waitNotif() // second notifications/initialized confirms re-init completed

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("listener not parked after re-init: %v", err)
	}
	// A leaked second listener parks right after its first GET; 200ms bounds waiting for it.
	ctx2, cancel2 := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel2()
	if clk.BlockUntilContext(ctx2, 2) == nil {
		t.Error("two listeners parked: re-init must not start a second notification listener")
	}

	clk.Advance(time.Second)

	select {
	case sid := <-m.getSeen:
		if err := clk.BlockUntilContext(ctx, 1); err != nil {
			t.Fatalf("listener did not re-sleep after reconnect: %v", err)
		}
		if sid == firstSID {
			t.Errorf("listener reconnected with expired session %q", firstSID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not reconnect after 1s advance")
	}
}

func waitUntilListenerParkedOnClock(t *testing.T, clk *clock.Fake) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("listener goroutine did not reach sleepCtx: %v", err)
	}
}
