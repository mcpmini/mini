//go:build test

package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sessionServer issues a new Mcp-Session-Id on every initialize and, while it
// issues sessions, answers other requests without one with 400, as the spec recommends.
type sessionServer struct {
	gets chan string

	mu          sync.Mutex
	seq         int
	current     string
	expired     map[string]bool
	expireAll   bool
	getNotFound bool
	sessionless bool
	requests    []sessionReq
}

type sessionReq struct {
	method    string
	sessionID string
}

func newSessionFake() *sessionServer {
	return &sessionServer{gets: make(chan string, 64), expired: map[string]bool{}}
}

func newSessionServer(t *testing.T) (*sessionServer, *httptest.Server) {
	t.Helper()
	m := newSessionFake()
	return m, newJSONRPCServer(t, m.handle)
}

func (m *sessionServer) handle(w http.ResponseWriter, r *http.Request) {
	sid := r.Header.Get("Mcp-Session-Id")
	if r.Method == http.MethodGet {
		m.serveNotificationStream(w, sid)
		return
	}
	var req map[string]any
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	method, _ := req["method"].(string)
	if status := m.admit(method, sid); status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	m.serveRPC(w, method, req["id"])
}

func (m *sessionServer) admit(method, sid string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, sessionReq{method: method, sessionID: sid})
	switch {
	case method == "initialize":
		return http.StatusOK
	case sid == "" && m.current != "":
		return http.StatusBadRequest
	case sid == "":
		return http.StatusOK
	case m.expired[sid], m.expireAll && method != NotificationInitialized:
		return http.StatusNotFound
	}
	return http.StatusOK
}

func (m *sessionServer) serveRPC(w http.ResponseWriter, method string, id any) {
	switch method {
	case "initialize":
		if sid := m.issueSession(); sid != "" {
			w.Header().Set("Mcp-Session-Id", sid)
		}
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"jsonrpc": "2.0", "id": id,
			"result": map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
			},
		})
	case NotificationInitialized:
		w.WriteHeader(http.StatusOK)
	default:
		w.Write(okRPCResponse(id)) //nolint:errcheck
	}
}

func (m *sessionServer) issueSession() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessionless {
		m.current = ""
		return ""
	}
	m.seq++
	m.current = fmt.Sprintf("s%d", m.seq)
	return m.current
}

func (m *sessionServer) serveNotificationStream(w http.ResponseWriter, sid string) {
	select {
	case m.gets <- sid:
	default:
	}
	m.mu.Lock()
	notFound := m.getNotFound
	m.mu.Unlock()
	if notFound {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (m *sessionServer) sessionID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

func (m *sessionServer) expireSession(sid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expired[sid] = true
}

func (m *sessionServer) rejectEverySessionAfterHandshake() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireAll = true
}

func (m *sessionServer) stopIssuingSessions() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionless = true
}

func (m *sessionServer) answerNotificationStreamWithNotFound() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getNotFound = true
}

func (m *sessionServer) awaitNotificationStream(t *testing.T) string {
	t.Helper()
	select {
	case sid := <-m.gets:
		return sid
	case <-time.After(3 * time.Second):
		t.Fatal("notification stream GET not received")
		return ""
	}
}

func (m *sessionServer) methods() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.requests))
	for i, r := range m.requests {
		out[i] = r.method
	}
	return out
}

func (m *sessionServer) initializeCount() int {
	return len(m.requestsWithMethod("initialize"))
}

func (m *sessionServer) requestsWithMethod(method string) []sessionReq {
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

func peekRPCMethod(r *http.Request) string {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	var req struct {
		Method string `json:"method"`
	}
	json.Unmarshal(body, &req) //nolint:errcheck
	return req.Method
}

func mustPing(t *testing.T, conn *HTTPConnection) {
	t.Helper()
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func pingInBackground(t *testing.T, conn *HTTPConnection) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := conn.Call(t.Context(), "ping", nil)
		done <- err
	}()
	return done
}

func awaitPing(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("background ping: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("background ping did not finish")
	}
}

// requestHold parks the first request that arrives after arm until release,
// or until the test ends so a failing test cannot leave the server blocked.
type requestHold struct {
	testDone <-chan struct{}
	armed    atomic.Bool
	held     chan struct{}
	released chan struct{}
}

func newRequestHold(t *testing.T) *requestHold {
	return &requestHold{testDone: t.Context().Done(), held: make(chan struct{}), released: make(chan struct{})}
}

func (h *requestHold) arm() { h.armed.Store(true) }

func (h *requestHold) blockIfArmed() bool {
	if !h.armed.CompareAndSwap(true, false) {
		return false
	}
	close(h.held)
	select {
	case <-h.released:
	case <-h.testDone:
	}
	return true
}

func (h *requestHold) awaitHeld(t *testing.T) {
	t.Helper()
	select {
	case <-h.held:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not hold the request")
	}
}

func (h *requestHold) release() { close(h.released) }
