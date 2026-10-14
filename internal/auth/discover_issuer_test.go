//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
)

func TestDiscover_rejectsInvalidIssuerAndMissingRequiredEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta func(string) map[string]any
	}{
		{name: "missing issuer", meta: func(string) map[string]any { return map[string]any{} }},
		{name: "mismatched issuer", meta: func(string) map[string]any { return map[string]any{"issuer": "https://attacker.example"} }},
		{name: "missing authorization endpoint", meta: func(issuer string) map[string]any {
			return map[string]any{"issuer": issuer, "token_endpoint": "https://as.example/token", "code_challenge_methods_supported": []string{"S256"}}
		}},
		{name: "missing token endpoint", meta: func(issuer string) map[string]any {
			return map[string]any{"issuer": issuer, "authorization_endpoint": "https://as.example/authorize", "code_challenge_methods_supported": []string{"S256"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/.well-known/oauth-authorization-server" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tc.meta("http://" + r.Host))
			}))
			defer srv.Close()
			_, err := auth.Discover(context.Background(), srv.URL)
			if err == nil {
				t.Fatal("Discover accepted invalid metadata")
			}
			if tc.name == "mismatched issuer" && !strings.Contains(err.Error(), "auth.client_id") {
				t.Fatalf("issuer mismatch error = %v, want own-client remedy", err)
			}
		})
	}
}

func TestDiscover_stopsAtFirstAnsweringInconsistentMetadata(t *testing.T) {
	secondCandidateReached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			secondCandidateReached = true
		}
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": "https://attacker.example", "authorization_endpoint": "https://as.example/authorize",
			"token_endpoint": "https://as.example/token", "code_challenge_methods_supported": []string{"S256"},
		})
	}))
	defer srv.Close()
	if _, err := auth.Discover(context.Background(), srv.URL); err == nil {
		t.Fatal("Discover accepted inconsistent first metadata response")
	}
	if secondCandidateReached {
		t.Fatal("Discover continued to a different metadata candidate after a 200 response")
	}
}

func TestDiscover_canonicalFallbackOriginAndTerminalIssuerSlash(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	uppercaseHost := strings.Replace(srv.URL, "127.0.0.1", "LOCALHOST", 1)
	meta, err := auth.Discover(context.Background(), uppercaseHost+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	want := "http://localhost" + strings.TrimPrefix(srv.URL, "http://127.0.0.1") + "/authorize"
	if meta.AuthURL != want {
		t.Errorf("fallback AuthURL = %q, want canonical origin %q", meta.AuthURL, want)
	}

	metadata := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint": "https://as.example/authorize", "token_endpoint": "https://as.example/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	defer metadata.Close()
	prm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{metadata.URL + "/"}})
	}))
	defer prm.Close()
	meta, err = auth.Discover(context.Background(), prm.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Issuer != metadata.URL {
		t.Errorf("metadata issuer = %q, want one-terminal-slash equivalent %q", meta.Issuer, metadata.URL)
	}
}

func TestDiscover_serverError_returnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := auth.Discover(context.Background(), srv.URL); err == nil {
		t.Error("expected error for 500 response, got nil")
	}
}

func TestDiscover_noPathURL(t *testing.T) {
	srv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	defer srv.Close()

	meta, err := auth.Discover(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if meta.AuthURL != "https://as.example.com/authorize" {
		t.Errorf("AuthURL: got %q", meta.AuthURL)
	}
}
