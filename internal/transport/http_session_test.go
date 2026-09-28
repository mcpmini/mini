//go:build test

package transport

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPSession_sessionExpiresTransparentRecovery(t *testing.T) {
	m, srv := newSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}

	m.expireSession(m.sessionID())
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("call after session expiry: %v", err)
	}

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
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}

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
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		switch req["method"] {
		case "initialize":
			writeInitializeResult(w, req["id"])
		case NotificationInitialized:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
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
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}

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
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}

	m.expireSession(m.sessionID())
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("call after re-init: %v", err)
	}

	if got := provider.refreshCount(); got != 1 {
		t.Errorf("auth refresh count = %d, want 1", got)
	}
	if got := m.initializeCount(); got != 2 {
		t.Errorf("sessions issued = %d, want 2", got)
	}
}

func TestHTTPSession_slowResponseEchoingTheOldSessionDoesNotReplaceTheNewOne(t *testing.T) {
	m := newSessionFake()
	var holdNextCall atomic.Bool
	held := make(chan struct{})
	release := make(chan struct{})
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && holdNextCall.CompareAndSwap(true, false) {
			close(held)
			<-release
			echoSessionWithOK(w, r)
			return
		}
		m.handle(w, r)
	})
	releaseHeldCall := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseHeldCall)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	holdNextCall.Store(true)
	slowDone := make(chan error, 1)
	go func() {
		_, err := conn.Call(t.Context(), "ping", nil)
		slowDone <- err
	}()
	select {
	case <-held:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not hold the slow call")
	}

	m.expireSession(m.sessionID())
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("re-init call: %v", err)
	}
	releaseHeldCall()
	if err := <-slowDone; err != nil {
		t.Fatalf("slow call: %v", err)
	}
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("call after the stale echo: %v", err)
	}

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
	var mu sync.Mutex
	var initializes int
	var pingSessions []string
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		sid := r.Header.Get("Mcp-Session-Id")
		mu.Lock()
		defer mu.Unlock()
		switch req["method"] {
		case "initialize":
			initializes++
			if initializes == 1 {
				w.Header().Set("Mcp-Session-Id", "s1")
			}
			writeInitializeResult(w, req["id"])
		case NotificationInitialized:
			w.WriteHeader(http.StatusOK)
		default:
			pingSessions = append(pingSessions, sid)
			if sid == "s1" && len(pingSessions) > 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write(okRPCResponse(req["id"])) //nolint:errcheck
		}
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	for range 3 {
		if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
			t.Fatalf("call: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if initializes != 2 {
		t.Fatalf("initialize count = %d, want 2", initializes)
	}
	for _, sid := range pingSessions[2:] {
		if sid != "" {
			t.Errorf("ping after a session-less re-init carried session %q, want none", sid)
		}
	}
}

func writeInitializeResult(w http.ResponseWriter, id any) {
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"jsonrpc": "2.0", "id": id,
		"result": map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{}},
	})
}
