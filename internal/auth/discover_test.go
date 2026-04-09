//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
)

func TestDiscover_fullMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"authorization_endpoint": "https://example.com/authorize",
			"token_endpoint":         "https://example.com/token",
			"registration_endpoint":  "https://example.com/register",
		})
	}))
	defer srv.Close()

	meta, err := auth.Discover(context.Background(), srv.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if meta.AuthURL != "https://example.com/authorize" {
		t.Errorf("AuthURL: got %q", meta.AuthURL)
	}
	if meta.TokenURL != "https://example.com/token" {
		t.Errorf("TokenURL: got %q", meta.TokenURL)
	}
	if meta.RegistrationURL != "https://example.com/register" {
		t.Errorf("RegistrationURL: got %q", meta.RegistrationURL)
	}
}

func TestDiscover_404_fallsBack(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	meta, err := auth.Discover(context.Background(), srv.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if meta.AuthURL != srv.URL+"/authorize" {
		t.Errorf("fallback AuthURL: got %q", meta.AuthURL)
	}
	if meta.TokenURL != srv.URL+"/token" {
		t.Errorf("fallback TokenURL: got %q", meta.TokenURL)
	}
	if meta.RegistrationURL != srv.URL+"/register" {
		t.Errorf("fallback RegistrationURL: got %q", meta.RegistrationURL)
	}
}

func TestDiscover_serverError_returnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := auth.Discover(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error for 500 response, got nil")
	}
}

func TestDiscover_noPathURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"authorization_endpoint": "https://example.com/authorize",
			"token_endpoint":         "https://example.com/token",
		})
	}))
	defer srv.Close()

	// URL with no path — should still work
	meta, err := auth.Discover(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if meta.AuthURL != "https://example.com/authorize" {
		t.Errorf("AuthURL: got %q", meta.AuthURL)
	}
}

func TestDiscover_invalidURL_returnsError(t *testing.T) {
	_, err := auth.Discover(context.Background(), "://bad-url")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}
