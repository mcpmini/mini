//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

func fakeMCPServer(t *testing.T, onRequest func(w http.ResponseWriter, r *http.Request, req map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		id := req["id"]
		method, _ := req["method"].(string)
		onRequest(w, r, req)
		if method == "initialize" {
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": id,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "s", "version": "0"},
				},
			})
		} else {
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"tools": []any{}}})
		}
	}))
}

func newServerForAuth(t *testing.T) *server.Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	return server.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestAuthHeader_bearer(t *testing.T) {
	var gotAuth string
	httpSrv := fakeMCPServer(t, func(w http.ResponseWriter, r *http.Request, req map[string]any) {
		gotAuth = r.Header.Get("Authorization")
	})
	defer httpSrv.Close()

	srv := newServerForAuth(t)
	sc := config.ServerConfig{
		Name:      "secured",
		Transport: "http",
		URL:       httpSrv.URL,
		Auth:      &config.AuthConfig{Type: "bearer", Token: "tok-123"},
	}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("AddUpstream with bearer auth failed: %v", err)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("expected Bearer tok-123, got: %q", gotAuth)
	}
}

func TestAuthHeader_apikey(t *testing.T) {
	var gotHeader string
	httpSrv := fakeMCPServer(t, func(w http.ResponseWriter, r *http.Request, req map[string]any) {
		gotHeader = r.Header.Get("X-Api-Key")
	})
	defer httpSrv.Close()

	srv := newServerForAuth(t)
	sc := config.ServerConfig{
		Name:      "api",
		Transport: "http",
		URL:       httpSrv.URL,
		Auth:      &config.AuthConfig{Type: "apikey", Header: "X-Api-Key", Token: "key-abc"},
	}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("AddUpstream with apikey auth failed: %v", err)
	}
	if gotHeader != "key-abc" {
		t.Errorf("expected key-abc in X-Api-Key, got: %q", gotHeader)
	}
}

func TestAuthHeader_emptyToken_noHeader(t *testing.T) {
	var gotAuth string
	httpSrv := fakeMCPServer(t, func(w http.ResponseWriter, r *http.Request, req map[string]any) {
		gotAuth = r.Header.Get("Authorization")
	})
	defer httpSrv.Close()

	srv := newServerForAuth(t)
	sc := config.ServerConfig{
		Name:      "noauth",
		Transport: "http",
		URL:       httpSrv.URL,
		Auth:      &config.AuthConfig{Type: "bearer", Token: ""},
	}
	if err := srv.AddUpstream(context.Background(), sc); err != nil {
		t.Fatalf("AddUpstream failed: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("expected no Authorization header with empty token, got: %q", gotAuth)
	}
}
