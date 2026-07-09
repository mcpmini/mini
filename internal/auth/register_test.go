//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

func TestRegister_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		if body["client_name"] != "mini" {
			t.Errorf("expected client_name=mini, got %v", body["client_name"])
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"client_id": "test-client-id"}) //nolint:errcheck
	}))
	defer srv.Close()

	clientID, err := auth.Register(context.Background(), srv.URL, auth.ResolvedCallbackURI(nil), "")
	if err != nil {
		t.Fatal(err)
	}
	if clientID != "test-client-id" {
		t.Errorf("expected test-client-id, got %q", clientID)
	}
}

// Strict servers like Atlassian exact-match redirect_uris, so DCR and the PKCE flow must use the same URI.
func TestRegister_redirectURIMatchesCallbackURI(t *testing.T) {
	cases := []struct {
		name        string
		callbackURI string
	}{
		{"default port", auth.ResolvedCallbackURI(nil)},
		{"custom port", "http://localhost:3118/callback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var registeredURIs []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					RedirectURIs []string `json:"redirect_uris"`
				}
				json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
				registeredURIs = body.RedirectURIs
				json.NewEncoder(w).Encode(map[string]string{"client_id": "cid"}) //nolint:errcheck
			}))
			defer srv.Close()

			if _, err := auth.Register(context.Background(), srv.URL, tc.callbackURI, ""); err != nil {
				t.Fatal(err)
			}
			if len(registeredURIs) != 1 || registeredURIs[0] != tc.callbackURI {
				t.Errorf("registered redirect_uris = %v, want [%s]", registeredURIs, tc.callbackURI)
			}
			parsed, err := url.Parse(registeredURIs[0])
			if err != nil {
				t.Fatalf("parse redirect_uri: %v", err)
			}
			if parsed.Path != auth.LoopbackCallbackPath {
				t.Errorf("redirect_uri path = %q, want %q", parsed.Path, auth.LoopbackCallbackPath)
			}
		})
	}
}

func TestRegister_200OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"client_id": "abc"}) //nolint:errcheck
	}))
	defer srv.Close()

	clientID, err := auth.Register(context.Background(), srv.URL, auth.ResolvedCallbackURI(nil), "")
	if err != nil {
		t.Fatal(err)
	}
	if clientID != "abc" {
		t.Errorf("got %q", clientID)
	}
}

func TestRegister_errorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := auth.Register(context.Background(), srv.URL, auth.ResolvedCallbackURI(nil), "")
	if err == nil {
		t.Error("expected error for 403 response")
	}
}

func TestRegister_emptyClientID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"client_id": ""}) //nolint:errcheck
	}))
	defer srv.Close()

	_, err := auth.Register(context.Background(), srv.URL, auth.ResolvedCallbackURI(nil), "")
	if err == nil {
		t.Error("expected error for empty client_id")
	}
}

func TestRegister_invalidURL(t *testing.T) {
	_, err := auth.Register(context.Background(), "://bad", auth.ResolvedCallbackURI(nil))
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestResolvedCallbackURI_usesConfiguredPort(t *testing.T) {
	ac := &config.AuthConfig{CallbackPort: 3118}
	uri := auth.ResolvedCallbackURI(ac)
	want := fmt.Sprintf("http://localhost:%d%s", 3118, auth.LoopbackCallbackPath)
	if uri != want {
		t.Errorf("ResolvedCallbackURI with CallbackPort=3118: got %q, want %q", uri, want)
	}
}

func TestResolvedCallbackURI_defaultPort(t *testing.T) {
	uri := auth.ResolvedCallbackURI(nil)
	want := fmt.Sprintf("http://localhost:%d%s", auth.LoopbackCallbackPort, auth.LoopbackCallbackPath)
	if uri != want {
		t.Errorf("ResolvedCallbackURI with nil: got %q, want %q", uri, want)
	}
}
