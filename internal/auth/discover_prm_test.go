//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
)

// serveASMeta returns an httptest.Server that serves OAuth AS metadata JSON at
// the given path and 404 for everything else.

func goodASMeta(host string) map[string]any {
	return map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
		"issuer":                           "http://" + host,
	}
}

func TestDiscover_prmOutcomes(t *testing.T) {
	cases := []struct {
		name         string
		prmStatus    int
		prmCT        string
		prmBody      string
		wantErr      bool
		wantFallback bool
	}{
		{
			name:      "429 rate limit",
			prmStatus: http.StatusTooManyRequests, prmCT: "text/plain", prmBody: "rate limited",
			wantErr: true,
		},
		{
			name:      "503 service unavailable",
			prmStatus: http.StatusServiceUnavailable, prmCT: "text/plain", prmBody: "unavailable",
			wantErr: true,
		},
		{
			name:      "200 JSON garbage",
			prmStatus: http.StatusOK, prmCT: "application/json", prmBody: `{not json`,
			wantErr: true,
		},
		{
			name:      "200 text/html",
			prmStatus: http.StatusOK, prmCT: "text/html", prmBody: `<html>catch-all</html>`,
			wantFallback: true,
		},
		{
			name:      "404 not found",
			prmStatus: http.StatusNotFound, prmCT: "text/plain", prmBody: "not found",
			wantFallback: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
					w.Header().Set("Content-Type", tc.prmCT)
					w.WriteHeader(tc.prmStatus)
					w.Write([]byte(tc.prmBody)) //nolint:errcheck
					return
				}
				http.NotFound(w, r)
			}))
			defer srv.Close()

			meta, err := auth.Discover(context.Background(), srv.URL+"/mcp")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tc.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.name, err)
			}
			if tc.wantFallback {
				if meta.AuthURL != srv.URL+"/authorize" {
					t.Errorf("fallback AuthURL: got %q, want %q", meta.AuthURL, srv.URL+"/authorize")
				}
			}
		})
	}
}

func TestDiscover_asMetaNetworkError(t *testing.T) {
	var asClosed atomic.Bool
	asSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if asClosed.Load() {
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "no hijack", http.StatusInternalServerError)
				return
			}
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(goodASMeta(r.Host)) //nolint:errcheck
	}))
	defer asSrv.Close()

	prmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"authorization_servers": []string{asSrv.URL},
		})
	}))
	defer prmSrv.Close()

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate",
			`Bearer resource_metadata="`+prmSrv.URL+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	asClosed.Store(true)
	_, err := auth.Discover(context.Background(), mcpSrv.URL+"/mcp")
	if err == nil {
		t.Fatal("expected error when AS metadata endpoint has network failure")
	}
}

func TestDiscover_asMetaNotFound(t *testing.T) {
	asSrv := httptest.NewServer(http.NotFoundHandler())
	defer asSrv.Close()

	prmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"authorization_servers": []string{asSrv.URL},
		})
	}))
	defer prmSrv.Close()

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate",
			`Bearer resource_metadata="`+prmSrv.URL+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	meta, err := auth.Discover(context.Background(), mcpSrv.URL+"/mcp")
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if meta.AuthURL != asSrv.URL+"/authorize" {
		t.Errorf("fallback AuthURL: got %q, want %q", meta.AuthURL, asSrv.URL+"/authorize")
	}
}

func TestDiscover_prmPathSpecific503RootValid(t *testing.T) {
	asSrv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	defer asSrv.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"authorization_servers": []string{asSrv.URL},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	meta, err := auth.Discover(context.Background(), srv.URL+"/mcp")
	if err != nil {
		t.Fatalf("expected success when root PRM is valid after path-specific 503: %v", err)
	}
	if meta.AuthURL != "https://as.example.com/authorize" {
		t.Errorf("AuthURL: got %q, want https://as.example.com/authorize", meta.AuthURL)
	}
}

func TestDiscover_prmPathSpecific503RootNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	_, err := auth.Discover(context.Background(), srv.URL+"/mcp")
	if err == nil {
		t.Fatal("expected error when path-specific PRM returns 503 and root PRM returns 404")
	}
}

func TestDiscover_prmValidJSONWithoutJSONContentTypeIsUsed(t *testing.T) {
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"authorization_endpoint":"` + "http://" + r.Host + `/authorize","token_endpoint":"` + "http://" + r.Host + `/token","code_challenge_methods_supported":["S256"]}`)) //nolint:errcheck
	}))
	defer as.Close()
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(`{"authorization_servers":["` + as.URL + `"]}`)) //nolint:errcheck
	}))
	defer mcp.Close()

	meta, err := auth.Discover(context.Background(), mcp.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if meta.TokenURL != as.URL+"/token" {
		t.Errorf("TokenURL = %q, want the advertised AS %q, not the MCP origin", meta.TokenURL, as.URL+"/token")
	}
}
