//go:build test

package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type sessionMCPServer struct {
	mu                        sync.Mutex
	sessionSeq                int
	currentSID                string
	expiredSIDs               map[string]bool
	allExpiredExceptHandshake bool
	alwaysNotFoundOnGET       bool
	notifInitialized          chan struct{}
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
		if m.alwaysNotFoundOnGET {
			m.requests = append(m.requests, sessionReq{method: "GET", sessionID: sid})
			if m.getSeen != nil {
				m.getSeen <- sid
			}
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
		m.currentSID = newSID
		w.Header().Set("Mcp-Session-Id", newSID)
		m.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"jsonrpc": "2.0", "id": req["id"],
			"result": map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
			},
		})
	case NotificationInitialized:
		if m.notifInitialized != nil {
			m.notifInitialized <- struct{}{}
		}
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
	var initCount int
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		method, _ := req["method"].(string)
		auth := r.Header.Get("Authorization")
		switch method {
		case "initialize":
			initCount++
			if auth != "Bearer new" {
				w.Header().Set("WWW-Authenticate", "Bearer realm=test")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Mcp-Session-Id", fmt.Sprintf("sid-%d", initCount))
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"jsonrpc": "2.0", "id": req["id"],
				"result": map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{}},
			})
		case NotificationInitialized:
			w.WriteHeader(http.StatusOK)
		default:
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
		t.Fatalf("expected success after auth retry on initialize, got: %v", err)
	}
	if got := provider.refreshCount(); got != 1 {
		t.Errorf("auth refresh count = %d, want 1", got)
	}
}

func TestCompareAndResetSession_keepsSessionIDSoRetriesNeverSendNone(t *testing.T) {
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: "http://127.0.0.1:1"})
	conn.storeSessionID("s1")
	conn.initialized = true

	conn.compareAndResetSession("s1")

	if conn.initialized {
		t.Error("reset must mark the connection uninitialized")
	}
	req, _, err := conn.buildHTTPRequest(t.Context(), Request{JSONRPC: "2.0", ID: 1, Method: "tools/call"})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Mcp-Session-Id"); got != "s1" {
		t.Errorf("a request rebuilt during re-init carried session %q, want the stale %q (never empty)", got, "s1")
	}
}

func TestBuildHTTPRequest_initializeNeverCarriesASession(t *testing.T) {
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: "http://127.0.0.1:1"})
	conn.storeSessionID("s1")

	req, _, err := conn.buildHTTPRequest(t.Context(), Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Mcp-Session-Id"); got != "" {
		t.Errorf("initialize carried session %q; a new session must start without one", got)
	}
}
