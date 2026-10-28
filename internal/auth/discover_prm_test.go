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
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
)

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

func TestDiscover_preservesPRMCandidateTrailingSlashAndQuery(t *testing.T) {
	tests := []struct {
		name      string
		serverURL string
		want      string
	}{
		{
			name:      "trailing slash",
			serverURL: "/mcp/",
			want:      "/.well-known/oauth-protected-resource/mcp/",
		},
		{
			name:      "query",
			serverURL: "/mcp?tenant=blue",
			want:      "/.well-known/oauth-protected-resource/mcp?tenant=blue",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var firstCandidate string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if firstCandidate == "" && strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
					firstCandidate = r.URL.RequestURI()
				}
				http.NotFound(w, r)
			}))
			defer srv.Close()

			if _, err := auth.Discover(context.Background(), srv.URL+tc.serverURL); err != nil {
				t.Fatalf("Discover: %v", err)
			}
			if firstCandidate != tc.want {
				t.Errorf("first PRM candidate = %q, want %q", firstCandidate, tc.want)
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

	var mcpResource string
	prmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"resource":              mcpResource,
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
	mcpResource = mcpSrv.URL + "/mcp"

	asClosed.Store(true)
	_, err := auth.Discover(context.Background(), mcpSrv.URL+"/mcp")
	if err == nil {
		t.Fatal("expected error when AS metadata endpoint has network failure")
	}
}

func TestDiscover_asMetaNotFound(t *testing.T) {
	asSrv := httptest.NewServer(http.NotFoundHandler())
	defer asSrv.Close()

	var mcpResource string
	prmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"resource":              mcpResource,
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
	mcpResource = mcpSrv.URL + "/mcp"

	meta, err := auth.Discover(context.Background(), mcpSrv.URL+"/mcp")
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if meta.AuthURL != asSrv.URL+"/authorize" {
		t.Errorf("fallback AuthURL: got %q, want %q", meta.AuthURL, asSrv.URL+"/authorize")
	}
}

func TestDiscover_prmPathSpecific503RootValid(t *testing.T) {
	asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	defer asSrv.Close()

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"resource":              srv.URL,
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
	if meta.Resource != srv.URL {
		t.Errorf("Resource = %q, want root resource %q", meta.Resource, srv.URL)
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

func TestDiscover_rejectsMissingOrMismatchedPRMResource(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resource func(string) string
	}{
		{name: "missing", resource: func(string) string { return "" }},
		{name: "different endpoint", resource: func(server string) string { return server + "/other" }},
		{name: "trailing slash", resource: func(server string) string { return server + "/mcp/" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var asRequests atomic.Int32
			as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				asRequests.Add(1)
				http.NotFound(w, r)
			}))
			defer as.Close()

			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/mcp" {
					w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+server.URL+`/metadata"`)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if r.URL.Path == "/metadata" {
					w.Header().Set("Content-Type", "application/json")
					meta := map[string]any{"authorization_servers": []string{as.URL}}
					if resource := tc.resource(server.URL); resource != "" {
						meta["resource"] = resource
					}
					json.NewEncoder(w).Encode(meta) //nolint:errcheck
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()

			_, err := auth.Discover(context.Background(), server.URL+"/mcp")
			if err == nil || !strings.Contains(err.Error(), "invalid protected resource metadata") {
				t.Fatalf("Discover error = %v, want PRM identity rejection", err)
			}
			if got := asRequests.Load(); got != 0 {
				t.Fatalf("authorization server requests = %d, want 0", got)
			}
		})
	}
}

func TestDiscover_prmValidJSONWithoutJSONContentTypeIsUsed(t *testing.T) {
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(
			[]byte(
				`{"issuer":"http://` + r.Host + `","authorization_endpoint":"` + "http://" + r.Host + `/authorize","token_endpoint":"` + "http://" + r.Host + `/token","code_challenge_methods_supported":["S256"]}`,
			),
		) //nolint:errcheck
	}))
	defer as.Close()
	var mcp *httptest.Server
	mcp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/.well-known/oauth-protected-resource") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write(
			[]byte(`{"resource":"` + mcp.URL + `/mcp","authorization_servers":["` + as.URL + `"]}`),
		) //nolint:errcheck
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

func TestDiscoverInvalidPathResourceUsesValidatedRoot(t *testing.T) {
	for _, tc := range []struct {
		name         string
		rootResource string
		wantError    bool
	}{
		{name: "valid root"},
		{name: "invalid root", rootResource: "https://other.example", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var badIssuerRequests atomic.Int32
			badIssuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				badIssuerRequests.Add(1)
				http.NotFound(w, r)
			}))
			defer badIssuer.Close()
			as := authtest.ServeASMeta(
				t,
				"/.well-known/oauth-authorization-server",
				map[string]any{
					"authorization_endpoint":           "https://as.example/authorize",
					"token_endpoint":                   "https://as.example/token",
					"code_challenge_methods_supported": []string{"S256"},
				},
			)
			defer as.Close()
			var mcp *httptest.Server
			mcp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource/mcp":
					json.NewEncoder(w).Encode(map[string]any{
						"resource": "https://other.example", "authorization_servers": []string{badIssuer.URL},
					})
				case "/.well-known/oauth-protected-resource":
					resource := tc.rootResource
					if resource == "" {
						resource = mcp.URL
					}
					json.NewEncoder(w).Encode(map[string]any{
						"resource": resource, "authorization_servers": []string{as.URL},
					})
				default:
					http.NotFound(w, r)
				}
			}))
			defer mcp.Close()
			meta, err := auth.Discover(t.Context(), mcp.URL+"/mcp")
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "invalid protected resource metadata") {
					t.Fatalf("Discover error = %v, want invalid PRM resource", err)
				}
			} else if err != nil || meta.Resource != mcp.URL || meta.Issuer != as.URL {
				t.Fatalf("Discover = %+v, %v; want validated root with issuer %s", meta, err, as.URL)
			}
			if got := badIssuerRequests.Load(); got != 0 {
				t.Fatalf("invalid path issuer received %d requests", got)
			}
		})
	}
}

func TestInvalidServerURLNeverFallsBackToOrigin(t *testing.T) {
	var requests atomic.Int32
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	defer mcp.Close()
	for _, serverURL := range []string{
		strings.Replace(mcp.URL, "://", "://user@", 1) + "/mcp",
		mcp.URL + "/mcp#fragment", "http:opaque", "ftp://example.test/mcp",
	} {
		t.Run(serverURL, func(t *testing.T) {
			if _, err := auth.Discover(t.Context(), serverURL); err == nil {
				t.Fatal("Discover accepted invalid server URL")
			}
			if auth.RequiresOAuth(t.Context(), config.ServerConfig{URL: serverURL}, "") {
				t.Fatal("invalid server URL confirmed OAuth")
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("invalid server URLs caused %d requests", got)
	}
}
