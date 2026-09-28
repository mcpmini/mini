//go:build test

package transport

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
)

func TestHTTPSession_sessionExpiresTransparentRecovery(t *testing.T) {
	m, srv := newSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	mustPing(t, conn)

	m.expireSession(m.sessionID())
	mustPing(t, conn)

	inits := m.requestsWithMethod("initialize")
	if len(inits) != 2 {
		t.Fatalf("initialize count = %d, want 2", len(inits))
	}
	if inits[1].sessionID != "" {
		t.Errorf("second initialize carried session %q, want none", inits[1].sessionID)
	}
	if got := len(m.requestsWithMethod(NotificationInitialized)); got != 2 {
		t.Errorf("notifications/initialized count = %d, want 2", got)
	}
	pings := m.requestsWithMethod("ping")
	if last := pings[len(pings)-1]; last.sessionID != m.sessionID() {
		t.Errorf("retried ping carried session %q, want %q", last.sessionID, m.sessionID())
	}
}

func TestHTTPSession_newSessionRejectedReturnsTypedError(t *testing.T) {
	m, srv := newSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	mustPing(t, conn)

	m.rejectEverySessionAfterHandshake()
	_, err := conn.Call(t.Context(), "ping", nil)

	var expiredErr *SessionExpiredError
	if !errors.As(err, &expiredErr) {
		t.Fatalf("err = %T %v, want *SessionExpiredError", err, err)
	}
	if got := m.initializeCount(); got != 2 {
		t.Errorf("initialize count = %d, want 2 (one re-init, no loop)", got)
	}
}

func TestHTTPSession_404WithoutSessionIsPlainError(t *testing.T) {
	m := newSessionFake()
	m.stopIssuingSessions()
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		if peekRPCMethod(r) == "ping" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		m.handle(w, r)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	_, err := conn.Call(t.Context(), "ping", nil)

	if err == nil {
		t.Fatal("expected 404 error")
	}
	var expiredErr *SessionExpiredError
	if errors.As(err, &expiredErr) {
		t.Error("got *SessionExpiredError; want plain error (no session was sent)")
	}
}

func TestHTTPSession_concurrentCallsOnExpiredSessionShareOneReinit(t *testing.T) {
	m, srv := newSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	mustPing(t, conn)

	m.expireSession(m.sessionID())
	const goroutines = 8
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for i := range goroutines {
		wg.Go(func() { _, errs[i] = conn.Call(t.Context(), "ping", nil) })
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
	if got := m.initializeCount(); got != 2 {
		t.Errorf("initialize count = %d, want 2 (one re-init for all concurrent callers)", got)
	}
}

func TestHTTPSession_401DuringReinitHandshakeRefreshesAndSucceeds(t *testing.T) {
	m := newSessionFake()
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		reinit := peekRPCMethod(r) == "initialize" && m.initializeCount() > 0
		if reinit && r.Header.Get("Authorization") != "Bearer new" {
			w.Header().Set("WWW-Authenticate", "Bearer realm=test")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		m.handle(w, r)
	})
	provider := &fakeAuthProvider{current: "Bearer old", next: "Bearer new"}
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, AuthProvider: provider, AuthHeaderName: "Authorization"})
	mustPing(t, conn)

	m.expireSession(m.sessionID())
	mustPing(t, conn)

	if got := provider.refreshCount(); got != 1 {
		t.Errorf("auth refresh count = %d, want 1", got)
	}
	if got := m.initializeCount(); got != 2 {
		t.Errorf("sessions issued = %d, want 2", got)
	}
}

func TestHTTPSession_slowResponseEchoingTheOldSessionDoesNotReplaceTheNewOne(t *testing.T) {
	m := newSessionFake()
	slow := newRequestHold(t)
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && slow.blockIfArmed() {
			echoSessionWithOK(w, r)
			return
		}
		m.handle(w, r)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	mustPing(t, conn)
	slow.arm()
	slowPing := pingInBackground(t, conn)
	slow.awaitHeld(t)

	m.expireSession(m.sessionID())
	mustPing(t, conn)
	slow.release()
	awaitPing(t, slowPing)
	mustPing(t, conn)

	if got := m.initializeCount(); got != 2 {
		t.Errorf("initialize count = %d, want 2 (a stored stale session forces another re-init)", got)
	}
}

func echoSessionWithOK(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	w.Header().Set("Mcp-Session-Id", r.Header.Get("Mcp-Session-Id"))
	w.Write(okRPCResponse(req["id"])) //nolint:errcheck
}

func TestHTTPSession_reinitWithoutASessionStopsSendingTheOldOne(t *testing.T) {
	m, srv := newSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	mustPing(t, conn)

	m.stopIssuingSessions()
	m.expireSession(m.sessionID())
	mustPing(t, conn)
	mustPing(t, conn)

	if got := m.initializeCount(); got != 2 {
		t.Fatalf("initialize count = %d, want 2", got)
	}
	pings := m.requestsWithMethod("ping")
	for _, p := range pings[len(pings)-2:] {
		if p.sessionID != "" {
			t.Errorf("ping after a session-less re-init carried session %q, want none", p.sessionID)
		}
	}
}
