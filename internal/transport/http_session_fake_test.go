//go:build test

package transport

import (
	"encoding/json"
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
	holdNextNonHandshake      chan struct{}
	holdSignal                chan string
	omitSessionOnNextInit     bool
	rejectMissingSession      bool
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
		m.handleGET(w, sid)
		return
	}
	m.requests = append(m.requests, sessionReq{method: method, sessionID: sid})
	isHandshake := method == "initialize" || method == NotificationInitialized
	if m.holdNextNonHandshake != nil && !isHandshake {
		m.serveHeld(w, req, sid)
		return
	}
	if m.rejectMissingSession && m.currentSID != "" && sid == "" && !isHandshake {
		m.mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	expired := sid != "" && (m.expiredSIDs[sid] || (m.allExpiredExceptHandshake && !isHandshake))
	m.mu.Unlock()
	if expired {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	m.serveRPC(w, req)
}

func (m *sessionMCPServer) handleGET(w http.ResponseWriter, sid string) {
	m.requests = append(m.requests, sessionReq{method: "GET", sessionID: sid})
	notFound := m.alwaysNotFoundOnGET
	if m.getSeen != nil {
		m.getSeen <- sid
	}
	m.mu.Unlock()
	if notFound {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (m *sessionMCPServer) serveHeld(w http.ResponseWriter, req map[string]any, sid string) {
	holdCh := m.holdNextNonHandshake
	holdSig := m.holdSignal
	m.holdNextNonHandshake = nil
	m.mu.Unlock()
	if holdSig != nil {
		holdSig <- sid
	}
	<-holdCh
	if sid != "" {
		w.Header().Set("Mcp-Session-Id", sid)
	}
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"jsonrpc": "2.0", "id": req["id"],
		"result": map[string]any{"ok": true},
	})
}

func (m *sessionMCPServer) serveRPC(w http.ResponseWriter, req map[string]any) {
	method, _ := req["method"].(string)
	switch method {
	case "initialize":
		m.mu.Lock()
		m.sessionSeq++
		newSID := fmt.Sprintf("s%d", m.sessionSeq)
		m.currentSID = newSID
		omit := m.omitSessionOnNextInit
		m.omitSessionOnNextInit = false
		m.mu.Unlock()
		if !omit {
			w.Header().Set("Mcp-Session-Id", newSID)
		}
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
