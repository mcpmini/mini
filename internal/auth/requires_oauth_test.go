//go:build test

package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
)

func TestRequiresOAuth_headerPresentShortCircuitsWithoutNetworkCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if !auth.RequiresOAuth(context.Background(), srv.URL+"/mcp", "Bearer") {
		t.Fatal("expected true when WWW-Authenticate header is present")
	}
	if called {
		t.Error("expected no network call when header already confirms OAuth")
	}
}

func TestRequiresOAuth_prmDocumentConfirmsOAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-protected-resource" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"authorization_servers":["https://as.example.com"]}`)) //nolint:errcheck
	}))
	defer srv.Close()

	if !auth.RequiresOAuth(context.Background(), srv.URL, "") {
		t.Fatal("expected true when a PRM document exists")
	}
}

func TestRequiresOAuth_noHeaderNoPRM_returnsFalse(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	if auth.RequiresOAuth(context.Background(), srv.URL, "") {
		t.Fatal("expected false when neither header nor PRM document confirms OAuth")
	}
}
