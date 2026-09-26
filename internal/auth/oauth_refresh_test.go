//go:build test

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
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
)

func TestRefresh_expiredToken_returnsNewTokenAndSendsResource(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	dir := t.TempDir()
	token := pkceToken(t, mock)
	if err := auth.Save(dir, "srv", token); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, _ := auth.Load(dir, "srv")
	loaded.Expiry = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	mock.AccessToken = "refreshed-access-token"
	ac := mock.AuthConfig()
	ac.ResourceURL = "https://resource.example.com/mcp"
	newTok, err := auth.Refresh(context.Background(), ac, loaded)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	mock.Mu.Lock()
	refreshed, resourceValues := mock.Refreshed, mock.ResourceValues
	mock.Mu.Unlock()
	if !refreshed {
		t.Error("expected /token to be called with grant_type=refresh_token")
	}
	if newTok.AccessToken != "refreshed-access-token" {
		t.Errorf("access token = %q, want %q", newTok.AccessToken, "refreshed-access-token")
	}
	if len(resourceValues) != 1 || resourceValues[0] != ac.ResourceURL {
		t.Errorf("resource values = %q, want [%q]", resourceValues, ac.ResourceURL)
	}
}

func TestRefresh_redirectResponse_failsWithoutFollowing(t *testing.T) {
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

func TestRefresh_authStyleFallback_sendsResourceOnBothAttempts(t *testing.T) {
	var resources []string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resources = append(resources, r.Form["resource"]...)
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

func TestExchangeCode_withResourceURL_sendsResourceToTokenEndpoint(t *testing.T) {
	var capturedResourceValues []string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.FormValue("grant_type") == "authorization_code" {
			capturedResourceValues = r.Form["resource"]
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"access_token": "tok", "refresh_token": "ref", "token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(tokenSrv.Close)

	const resourceURL = "https://resource.example.com/mcp"
	ac := &config.AuthConfig{
		Type:        "oauth2",
		ClientID:    "client",
		AuthURL:     tokenSrv.URL + "/authorize",
		TokenURL:    tokenSrv.URL + "/token",
		ResourceURL: resourceURL,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	token, err := auth.PKCEFlow(ctx, ac, simulateBrowser)
	if err != nil {
		t.Fatalf("PKCEFlow: %v", err)
	}
	if token.AccessToken != "tok" {
		t.Errorf("access token = %q, want tok", token.AccessToken)
	}
	if len(capturedResourceValues) != 1 || capturedResourceValues[0] != resourceURL {
		t.Errorf("resource values at token endpoint = %q, want [%q]", capturedResourceValues, resourceURL)
	}
}
