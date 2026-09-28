//go:build test

package transport

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestHTTPSession_get404NeverTriggersReinit(t *testing.T) {
	m, srv := newSessionServer(t)
	m.answerNotificationStreamWithNotFound()
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	for range 5 {
		mustPing(t, conn)
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
	mustPing(t, conn)
	m.awaitNotificationStream(t)
	for _, backoff := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		advanceListenerTimerAndAwaitNextSleep(t, clk, backoff)
		m.awaitNotificationStream(t)
	}

	m.expireSession(m.sessionID())
	mustPing(t, conn)

	if got := m.awaitNotificationStream(t); got != m.sessionID() {
		t.Errorf("listener reconnected with session %q, want %q", got, m.sessionID())
	}
}

func TestHTTPSession_reinitKeepsASingleNotificationListener(t *testing.T) {
	m, srv := newSessionServer(t)
	clk := clock.NewFake()
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clk})
	mustPing(t, conn)
	m.awaitNotificationStream(t)
	awaitListenerTimer(t, clk)

	m.expireSession(m.sessionID())
	mustPing(t, conn)
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
	reinit := newRequestHold(t)
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		if peekRPCMethod(r) == "initialize" {
			reinit.blockIfArmed()
		}
		m.handle(w, r)
	})
	clk := clock.NewFake()
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clk})
	mustPing(t, conn)
	oldSID := m.awaitNotificationStream(t)
	awaitListenerTimer(t, clk)

	reinit.arm()
	m.expireSession(oldSID)
	reinitPing := pingInBackground(t, conn)
	reinit.awaitHeld(t)
	clk.Advance(time.Second)

	if got := m.awaitNotificationStream(t); got != oldSID {
		t.Errorf("listener request during re-init carried session %q, want %q", got, oldSID)
	}
	reinit.release()
	awaitPing(t, reinitPing)
}
