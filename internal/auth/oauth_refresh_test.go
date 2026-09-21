package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

func TestRefresh(t *testing.T) {
	mock := newMockAuthServer(t)
	dir := t.TempDir()
	token := pkceToken(t, mock)
	if err := auth.Save(dir, "srv", token); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, _ := auth.Load(dir, "srv")
	loaded.Expiry = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	mock.accessToken = "refreshed-access-token"
	ac := mock.authConfig()
	ac.ResourceURL = "https://resource.example.com/mcp"
	newTok, err := auth.Refresh(context.Background(), ac, loaded)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !mock.refreshed {
		t.Error("expected /token to be called with grant_type=refresh_token")
	}
	if newTok.AccessToken != "refreshed-access-token" {
		t.Errorf("access token = %q, want %q", newTok.AccessToken, "refreshed-access-token")
	}
	if mock.resourceURL != ac.ResourceURL {
		t.Errorf("resource = %q, want %q", mock.resourceURL, ac.ResourceURL)
	}
}

func TestRefresh_resourcePreservesAuthStyleFallback(t *testing.T) {
	var resources []string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resources = append(resources, r.FormValue("resource"))
		if _, _, basic := r.BasicAuth(); basic {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.FormValue("client_id") != "client" || r.FormValue("client_secret") != "secret" {
			http.Error(w, "missing client credentials", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": "new-access", "refresh_token": "rotated-refresh", "token_type": "Bearer",
		})
	}))
	t.Cleanup(tokenServer.Close)
	const resource = "https://resource.example.com/mcp"
	ac := &config.AuthConfig{
		ClientID: "client", ClientSecret: "secret", TokenURL: tokenServer.URL, ResourceURL: resource,
	}
	token := &oauth2.Token{AccessToken: "expired", RefreshToken: "old-refresh", Expiry: time.Now().Add(-time.Hour)}
	got, err := auth.Refresh(context.Background(), ac, token)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got.RefreshToken != "rotated-refresh" {
		t.Fatalf("refresh token = %q, want rotated token", got.RefreshToken)
	}
	if len(resources) != 2 || resources[0] != resource || resources[1] != resource {
		t.Fatalf("resource values = %q, want resource on both auth-style attempts", resources)
	}
}

func TestRefreshDoesNotFollowRedirect(t *testing.T) {
	targetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetCalled = true
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	ac := &config.AuthConfig{ClientID: "client", TokenURL: redirect.URL}
	token := &oauth2.Token{AccessToken: "expired", RefreshToken: "refresh", Expiry: time.Now().Add(-time.Hour)}
	if _, err := auth.Refresh(context.Background(), ac, token); err == nil {
		t.Fatal("expected redirected token refresh to fail")
	}
	if targetCalled {
		t.Error("token refresh followed redirect")
	}
}
