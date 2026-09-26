package authtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

type TokenServer struct {
	Srv          *httptest.Server
	AccessToken  string
	RefreshToken string

	Hits   atomic.Int32
	Status atomic.Int32

	Mu             sync.Mutex
	Refreshed      bool
	ResourceValues []string
	LastGrant      string
	LastRefresh    string
	LastClientID   string
	LastBasicAuth  string
	LastResource   string
	HoldReady      chan struct{}
	HoldGate       chan struct{}
	OverrideStatus int
	OverrideBody   []byte
}

func NewTokenServer(t *testing.T) *TokenServer {
	t.Helper()
	m := &TokenServer{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
	}
	m.Status.Store(http.StatusOK)
	mux := http.NewServeMux()
	mux.HandleFunc("/token", m.handleToken)
	m.Srv = httptest.NewServer(mux)
	t.Cleanup(m.Srv.Close)
	return m
}

func (m *TokenServer) handleToken(w http.ResponseWriter, r *http.Request) {
	m.Hits.Add(1)
	if m.serveOverride(w) {
		return
	}
	if status := int(m.Status.Load()); status != http.StatusOK {
		http.Error(w, "refresh rejected", status)
		return
	}
	m.waitForGate()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	m.captureRequestFields(r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"access_token": m.AccessToken, "refresh_token": m.RefreshToken,
		"token_type": "Bearer", "expires_in": 3600,
	})
}

func (m *TokenServer) serveOverride(w http.ResponseWriter) bool {
	m.Mu.Lock()
	overrideStatus, overrideBody := m.OverrideStatus, m.OverrideBody
	m.Mu.Unlock()
	if overrideBody == nil {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(overrideStatus)
	w.Write(overrideBody) //nolint:errcheck
	return true
}

func (m *TokenServer) waitForGate() {
	m.Mu.Lock()
	ready, gate := m.HoldReady, m.HoldGate
	m.HoldReady, m.HoldGate = nil, nil
	m.Mu.Unlock()
	if ready != nil {
		close(ready)
		<-gate
	}
}

func (m *TokenServer) captureRequestFields(r *http.Request) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if r.FormValue("grant_type") == "refresh_token" {
		m.Refreshed = true
		m.ResourceValues = r.Form["resource"]
	}
	m.LastGrant = r.FormValue("grant_type")
	m.LastRefresh = r.FormValue("refresh_token")
	m.LastClientID = r.FormValue("client_id")
	m.LastResource = r.FormValue("resource")
	if user, _, ok := r.BasicAuth(); ok {
		m.LastBasicAuth = user
	}
}

func (m *TokenServer) AuthConfig() *config.AuthConfig {
	return &config.AuthConfig{
		Type:     "oauth2",
		ClientID: "test-client-id",
		AuthURL:  m.Srv.URL + "/authorize",
		TokenURL: m.Srv.URL + "/token",
	}
}

func (m *TokenServer) RespondWith(status int, body string) {
	m.Mu.Lock()
	m.OverrideStatus = status
	m.OverrideBody = []byte(body)
	m.Mu.Unlock()
}
