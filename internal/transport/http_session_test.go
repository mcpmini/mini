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
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

type sessionMCPServer struct {
	mu                        sync.Mutex
	sessionSeq                int
	currentSID                string
	expiredSIDs               map[string]bool
	allExpiredExceptHandshake bool
	noSession                 bool
	notFoundOnFirstGETOnly    bool
	getSeen                   chan string
	requests                  []sessionReq
}

type sessionReq struct {
	method    string
	sessionID string
}

func newSessionServer(t *testing.T) (*sessionMCPServer, *httptest.Server) {
	t.Helper()
	m := &sessionMCPServer{expiredSIDs: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(srv.Close)
	return m, srv
}

func (m *sessionMCPServer) handle(w http.ResponseWriter, r *http.Request) {
	sid := r.Header.Get("Mcp-Session-Id")

	m.mu.Lock()
	var req map[string]any
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	method, _ := req["method"].(string)
	if r.Method == http.MethodGet {
		method = "GET"
		if sid != "" && m.notFoundOnFirstGETOnly {
			m.notFoundOnFirstGETOnly = false
			m.requests = append(m.requests, sessionReq{method: "GET", sessionID: sid})
			m.mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}
	m.requests = append(m.requests, sessionReq{method: method, sessionID: sid})
	if method == "GET" && m.getSeen != nil {
		m.getSeen <- sid
	}
	isHandshake := method == "initialize" || method == NotificationInitialized
	expired := sid != "" && (m.expiredSIDs[sid] || (m.allExpiredExceptHandshake && !isHandshake))
	m.mu.Unlock()

	if expired {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		w.WriteHeader(http.StatusOK)
		return
	}
	m.serveRPC(w, req)
}

func (m *sessionMCPServer) serveRPC(w http.ResponseWriter, req map[string]any) {
	method, _ := req["method"].(string)
	switch method {
	case "initialize":
		m.mu.Lock()
		m.sessionSeq++
		newSID := fmt.Sprintf("s%d", m.sessionSeq)
		if !m.noSession {
			m.currentSID = newSID
			w.Header().Set("Mcp-Session-Id", newSID)
		}
		m.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"jsonrpc": "2.0", "id": req["id"],
			"result": map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
			},
		})
	case NotificationInitialized:
		w.WriteHeader(http.StatusOK)
	default:
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"jsonrpc": "2.0", "id": req["id"],
			"result": map[string]any{"ok": true},
		})
	}
}

func (m *sessionMCPServer) expireSession(sid string) {
	m.mu.Lock()
	m.expiredSIDs[sid] = true
	m.mu.Unlock()
}

func (m *sessionMCPServer) expireAllSessionsExceptHandshake() {
	m.mu.Lock()
	m.allExpiredExceptHandshake = true
	m.mu.Unlock()
}

func (m *sessionMCPServer) initializeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, r := range m.requests {
		if r.method == "initialize" {
			count++
		}
	}
	return count
}

func (m *sessionMCPServer) requestsWithMethod(method string) []sessionReq {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []sessionReq
	for _, r := range m.requests {
		if r.method == method {
			out = append(out, r)
		}
	}
	return out
}

func waitUntilListenerParkedOnClock(t *testing.T, clk *clock.Fake) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := clk.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("listener goroutine did not reach sleepCtx: %v", err)
	}
}

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
	if len(inits) < 2 || inits[1].sessionID != "" {
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
	if len(pings) == 0 || pings[len(pings)-1].sessionID != sid2 {
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

func TestHTTPSession_listenerStream404_nextCallReinitializes(t *testing.T) {
	m, srv := newSessionServer(t)
	m.mu.Lock()
	m.notFoundOnFirstGETOnly = true
	m.mu.Unlock()

	clk := clock.NewFake()
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clk})

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}

	waitUntilListenerParkedOnClock(t, clk)

	conn.initMu.Lock()
	initialized := conn.initialized
	conn.initMu.Unlock()
	if initialized {
		t.Error("expected initialized=false after listener 404")
	}

	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("call after listener reset: %v", err)
	}
	if got := m.initializeCount(); got < 2 {
		t.Errorf("initialize count = %d, want >= 2", got)
	}
}

func TestHTTPSession_reinitKeepsASingleNotificationListener(t *testing.T) {
	m, srv := newSessionServer(t)
	m.getSeen = make(chan string, 16)
	clk := clock.NewFake()
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clk})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	firstSID := <-m.getSeen
	waitUntilListenerParkedOnClock(t, clk)

	m.expireSession(firstSID)
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("call after session expiry: %v", err)
	}
	select {
	case sid := <-m.getSeen:
		t.Fatalf("re-init started a second notification listener (stream opened with session %q)", sid)
	case <-time.After(300 * time.Millisecond):
	}

	clk.Advance(time.Hour)
	if next := <-m.getSeen; next == firstSID {
		t.Errorf("listener reconnected with the expired session %q", firstSID)
	}
}

func TestHTTPSession_401StillGoesThrough(t *testing.T) {
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
			if r.Header.Get("Authorization") == "Bearer old" {
				w.Header().Set("WWW-Authenticate", "Bearer realm=test")
				w.WriteHeader(http.StatusUnauthorized)
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
		t.Fatalf("expected success after auth retry, got: %v", err)
	}
	if got := provider.refreshCount(); got != 1 {
		t.Errorf("auth refresh count = %d, want 1", got)
	}
}
