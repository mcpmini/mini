//go:build test

package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestHTTPSession_sessionExpiresTransparentRecovery(t *testing.T) {
	m, srv := newSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	m.mu.Lock()
	sid1 := m.currentSID
	m.mu.Unlock()
	m.expireSession(sid1)

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("call after session expiry: %v", err)
	}

	if got := m.initializeCount(); got != 2 {
		t.Errorf("initialize count = %d, want 2", got)
	}

	inits := m.requestsWithMethod("initialize")
	if len(inits) < 2 {
		t.Fatalf("expected at least 2 initialize requests, got %d", len(inits))
	}
	if inits[1].sessionID != "" {
		t.Errorf("second initialize carried session header %q, want none", inits[1].sessionID)
	}

	notifCount := len(m.requestsWithMethod(NotificationInitialized))
	if notifCount < 2 {
		t.Errorf("notifications/initialized count = %d, want >= 2", notifCount)
	}

	pings := m.requestsWithMethod("ping")
	m.mu.Lock()
	sid2 := m.currentSID
	m.mu.Unlock()
	if len(pings) == 0 {
		t.Fatalf("expected at least 1 ping, got 0")
	}
	if pings[len(pings)-1].sessionID != sid2 {
		t.Errorf("last ping used session %q, want %q", pings[len(pings)-1].sessionID, sid2)
	}
}

func TestHTTPSession_newSessionRejectedReturnsTypedError(t *testing.T) {
	m, srv := newSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	m.mu.Lock()
	sid1 := m.currentSID
	m.mu.Unlock()
	m.expireSession(sid1)
	m.expireAllSessionsExceptHandshake()

	_, err := conn.Call(t.Context(), "ping", nil)
	if err == nil {
		t.Fatal("expected error after persistent session rejection")
	}
	var expiredErr *SessionExpiredError
	if !errors.As(err, &expiredErr) {
		t.Fatalf("expected *SessionExpiredError, got %T: %v", err, err)
	}
	if got := m.initializeCount(); got != 2 {
		t.Errorf("initialize count = %d, want 2 (no loop)", got)
	}
}

func TestHTTPSession_404WithoutSessionIsPlainError(t *testing.T) {
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		method, _ := req["method"].(string)
		switch method {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{}},
			})
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

func TestHTTPSession_concurrentCallsOnExpiredSession(t *testing.T) {
	m, srv := newSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	m.mu.Lock()
	sid1 := m.currentSID
	m.mu.Unlock()
	m.expireSession(sid1)

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = conn.Call(t.Context(), "ping", nil)
		}(i)
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
	var mu sync.Mutex
	sessionSeq := 0
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		method, _ := req["method"].(string)
		auth := r.Header.Get("Authorization")
		sid := r.Header.Get("Mcp-Session-Id")
		switch method {
		case "initialize":
			mu.Lock()
			isReinit := sessionSeq >= 1
			mu.Unlock()
			if isReinit && auth != "Bearer new" {
				w.Header().Set("WWW-Authenticate", "Bearer realm=test")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			mu.Lock()
			sessionSeq++
			thisSID := fmt.Sprintf("sid-%d", sessionSeq)
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", thisSID)
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{}},
			})
		case NotificationInitialized:
			w.WriteHeader(http.StatusOK)
		default:
			if sid == "sid-1" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"ok": true},
			})
		}
	})

	provider := &fakeAuthProvider{current: "Bearer old", next: "Bearer new"}
	conn := mustHTTPConn(t, HTTPConnectionConfig{
		URL:            srv.URL,
		AuthProvider:   provider,
		AuthHeaderName: "Authorization",
	})

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("call after re-init: %v", err)
	}
	if got := provider.refreshCount(); got != 1 {
		t.Errorf("auth refresh count = %d, want 1", got)
	}
	mu.Lock()
	if sessionSeq != 2 {
		t.Errorf("sessions issued = %d, want 2", sessionSeq)
	}
	mu.Unlock()
}

func TestHTTPSession_stragglerRequestsCarrySessionDuringReinit(t *testing.T) {
	m, srv := newSessionServer(t)
	m.mu.Lock()
	m.rejectMissingSession = true
	m.mu.Unlock()

	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	m.mu.Lock()
	sid1 := m.currentSID
	m.mu.Unlock()
	m.expireSession(sid1)

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = conn.Call(t.Context(), "ping", nil)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}

func TestHTTPSession_staleEchoAfterReinit(t *testing.T) {
	m, srv := newSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	m.mu.Lock()
	sid1 := m.currentSID
	m.mu.Unlock()

	gate := make(chan struct{})
	holdSig := make(chan string, 1)
	m.mu.Lock()
	m.holdNextNonHandshake = gate
	m.holdSignal = holdSig
	m.mu.Unlock()

	slowDone := make(chan error, 1)
	go func() {
		_, err := conn.Call(t.Context(), "ping", nil)
		slowDone <- err
	}()
	select {
	case <-holdSig:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not capture the slow call")
	}

	m.expireSession(sid1)
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("re-init call: %v", err)
	}

	close(gate)
	if err := <-slowDone; err != nil {
		t.Fatalf("slow call: %v", err)
	}

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("post-stale call: %v", err)
	}
	m.mu.Lock()
	sid2 := m.currentSID
	m.mu.Unlock()
	pings := m.requestsWithMethod("ping")
	if last := pings[len(pings)-1]; last.sessionID != sid2 {
		t.Errorf("last ping carried session %q, want %q", last.sessionID, sid2)
	}
	if got := m.initializeCount(); got != 2 {
		t.Errorf("initialize count = %d, want 2", got)
	}
}

func TestHTTPSession_reinitNoSession(t *testing.T) {
	m, srv := newSessionServer(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	m.mu.Lock()
	sid1 := m.currentSID
	m.omitSessionOnNextInit = true
	m.mu.Unlock()

	m.expireSession(sid1)
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("call after session expiry: %v", err)
	}
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("third call: %v", err)
	}

	pings := m.requestsWithMethod("ping")
	for _, p := range pings[len(pings)-2:] {
		if p.sessionID != "" {
			t.Errorf("ping after no-session re-init carried session %q, want none", p.sessionID)
		}
	}
	if got := m.initializeCount(); got != 2 {
		t.Errorf("initialize count = %d, want 2", got)
	}
}
