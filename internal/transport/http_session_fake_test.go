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
	"testing"
	"time"
)

// sessionServer issues a new Mcp-Session-Id on every initialize and answers other
// requests without a session with 400, as the spec recommends.
type sessionServer struct {
	gets chan string

	mu          sync.Mutex
	seq         int
	current     string
	expired     map[string]bool
	expireAll   bool
	getNotFound bool
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
	case sid == "":
		return http.StatusBadRequest
	case m.expired[sid], m.expireAll && method != NotificationInitialized:
		return http.StatusNotFound
	}
	return http.StatusOK
}

func (m *sessionServer) serveRPC(w http.ResponseWriter, method string, id any) {
	switch method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", m.issueSession())
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
