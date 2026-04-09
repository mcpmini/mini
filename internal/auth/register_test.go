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

	clientID, err := auth.Register(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if clientID != "test-client-id" {
		t.Errorf("expected test-client-id, got %q", clientID)
	}
}

func TestRegister_200OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"client_id": "abc"}) //nolint:errcheck
	}))
	defer srv.Close()

	clientID, err := auth.Register(context.Background(), srv.URL)
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

	_, err := auth.Register(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error for 403 response")
	}
}

func TestRegister_emptyClientID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"client_id": ""}) //nolint:errcheck
	}))
	defer srv.Close()

	_, err := auth.Register(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error for empty client_id")
	}
}

func TestRegister_invalidURL(t *testing.T) {
	_, err := auth.Register(context.Background(), "://bad")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}
