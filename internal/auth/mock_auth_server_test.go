package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

type mockAuthServer struct {
	srv          *httptest.Server
	accessToken  string
	refreshToken string

	hits   atomic.Int32
	status atomic.Int32

	mu             sync.Mutex
	refreshed      bool
	resourceValues []string
	lastGrant      string
	lastRefresh    string
	lastClientID   string
	lastBasicAuth  string
	lastResource   string
	holdReady      chan struct{}
	holdGate       chan struct{}
}

func newMockAuthServer(t *testing.T) *mockAuthServer {
	t.Helper()
	m := &mockAuthServer{
		accessToken:  "test-access-token",
		refreshToken: "test-refresh-token",
	}
	m.status.Store(http.StatusOK)
	mux := http.NewServeMux()
	mux.HandleFunc("/token", m.handleToken)
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockAuthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	m.hits.Add(1)
	if status := int(m.status.Load()); status != http.StatusOK {
		http.Error(w, "refresh rejected", status)
		return
	}
	m.mu.Lock()
	ready, gate := m.holdReady, m.holdGate
	m.holdReady, m.holdGate = nil, nil
	m.mu.Unlock()
	if ready != nil {
		close(ready)
		<-gate
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	if r.FormValue("grant_type") == "refresh_token" {
		m.refreshed = true
		m.resourceValues = r.Form["resource"]
	}
	m.lastGrant = r.FormValue("grant_type")
	m.lastRefresh = r.FormValue("refresh_token")
	m.lastClientID = r.FormValue("client_id")
	m.lastResource = r.FormValue("resource")
	if user, _, ok := r.BasicAuth(); ok {
		m.lastBasicAuth = user
	}
	m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"access_token": m.accessToken, "refresh_token": m.refreshToken,
		"token_type": "Bearer", "expires_in": 3600,
	})
}

func (m *mockAuthServer) authConfig() *config.AuthConfig {
	return &config.AuthConfig{
		Type:     "oauth2",
		ClientID: "test-client-id",
		AuthURL:  m.srv.URL + "/authorize",
		TokenURL: m.srv.URL + "/token",
	}
}
