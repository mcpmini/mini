package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

type mockAuthServer struct {
	srv          *httptest.Server
	accessToken  string
	refreshToken string
	refreshed    bool
	resourceURL  string
}

func newMockAuthServer(t *testing.T) *mockAuthServer {
	t.Helper()
	m := &mockAuthServer{
		accessToken:  "test-access-token",
		refreshToken: "test-refresh-token",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", m.handleToken)
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockAuthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.FormValue("grant_type") == "refresh_token" {
		m.refreshed = true
		m.resourceURL = r.FormValue("resource")
		m.accessToken = "refreshed-access-token"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token":  m.accessToken,
		"refresh_token": m.refreshToken,
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

func (m *mockAuthServer) authConfig() *config.AuthConfig {
	return &config.AuthConfig{
		Type:     "oauth2",
		ClientID: "test-client-id",
		AuthURL:  m.srv.URL + "/authorize", // doesn't need to exist; we skip it
		TokenURL: m.srv.URL + "/token",
	}
}

func simulateBrowser(authURL string) error {
	parsed, err := url.Parse(authURL)
	if err != nil {
		return err
	}
	q := parsed.Query()
	state := q.Get("state")
	redirectURI := q.Get("redirect_uri")

	callbackURL := redirectURI + "?code=test-auth-code&state=" + url.QueryEscape(state)
	go http.Get(callbackURL) //nolint:errcheck
	return nil
}

func pkceToken(t *testing.T, mock *mockAuthServer) *oauth2.Token {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	token, err := auth.PKCEFlow(ctx, mock.authConfig(), simulateBrowser)
	if err != nil {
		t.Fatalf("PKCEFlow: %v", err)
	}
	return token
}
