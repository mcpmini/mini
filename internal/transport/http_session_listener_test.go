//go:build test

package transport

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestHTTPSession_get404NeverTriggersReinit(t *testing.T) {
	m, srv := newSessionServer(t)
	m.answerNotificationStreamWithNotFound()
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	for range 5 {
		if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
			t.Fatalf("call failed: %v", err)
		}
	}

	if got := m.initializeCount(); got != 1 {
		t.Errorf("initialize count = %d, want 1 (GET 404 must not trigger re-init)", got)
	}
	for _, p := range m.requestsWithMethod("ping") {
		if p.sessionID != m.sessionID() {
			t.Errorf("ping carried session %q, want %q", p.sessionID, m.sessionID())
		}
	}
}

func TestHTTPSession_listenerReconnectsImmediatelyAfterReinit(t *testing.T) {
	m, srv := newSessionServer(t)
	m.answerNotificationStreamWithNotFound()
	clk := clock.NewFake()
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clk})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	m.awaitNotificationStream(t)
	for _, backoff := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		advanceListenerTimerAndAwaitNextSleep(t, clk, backoff)
		m.awaitNotificationStream(t)
	}

	m.expireSession(m.sessionID())
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("re-init call: %v", err)
	}

	if got := m.awaitNotificationStream(t); got != m.sessionID() {
		t.Errorf("listener reconnected with session %q, want %q", got, m.sessionID())
	}
}

func TestHTTPSession_reinitKeepsASingleNotificationListener(t *testing.T) {
	m, srv := newSessionServer(t)
	clk := clock.NewFake()
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clk})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	m.awaitNotificationStream(t)
	awaitListenerTimer(t, clk)

	m.expireSession(m.sessionID())
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("call after session expiry: %v", err)
	}
	if got := m.awaitNotificationStream(t); got != m.sessionID() {
		t.Fatalf("listener reconnected with session %q, want %q", got, m.sessionID())
	}
	awaitListenerTimer(t, clk)

	// A leaked second listener parks right after its first GET; 200ms bounds waiting for it.
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if clk.BlockUntilContext(ctx, 2) == nil {
		t.Error("two listeners parked: re-init must not start a second notification listener")
	}
}

func TestHTTPSession_requestsSentWhileReinitializingKeepTheOldSession(t *testing.T) {
	m := newSessionFake()
	var holdNextInitialize atomic.Bool
	initializeHeld := make(chan struct{})
	release := make(chan struct{})
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		if peekRPCMethod(r) == "initialize" && holdNextInitialize.CompareAndSwap(true, false) {
			close(initializeHeld)
			<-release
		}
		m.handle(w, r)
	})
	releaseInitialize := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseInitialize)
	clk := clock.NewFake()
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clk})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	oldSID := m.awaitNotificationStream(t)
	awaitListenerTimer(t, clk)

	holdNextInitialize.Store(true)
	m.expireSession(oldSID)
	callDone := make(chan error, 1)
	go func() {
		_, err := conn.Call(t.Context(), "ping", nil)
		callDone <- err
	}()
	select {
	case <-initializeHeld:
	case <-time.After(3 * time.Second):
		t.Fatal("re-initialize not received")
	}
	clk.Advance(time.Second)

	if got := m.awaitNotificationStream(t); got != oldSID {
		t.Errorf("listener request during re-init carried session %q, want %q", got, oldSID)
	}
	releaseInitialize()
	if err := <-callDone; err != nil {
		t.Fatalf("call that triggered re-init: %v", err)
	}
}
