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
)

// serveASMeta returns an httptest.Server that serves OAuth AS metadata JSON at
// the given path and 404 for everything else.
func serveASMeta(t *testing.T, path string, meta map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(meta) //nolint:errcheck
	}))
}

func TestDiscover_rootASMeta(t *testing.T) {
	srv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint": "https://as.example.com/authorize",
		"token_endpoint":         "https://as.example.com/token",
		"registration_endpoint":  "https://as.example.com/register",
	})
	defer srv.Close()

	meta, err := auth.Discover(context.Background(), srv.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if meta.AuthURL != "https://as.example.com/authorize" {
		t.Errorf("AuthURL: got %q", meta.AuthURL)
	}
	if meta.RegistrationURL != "https://as.example.com/register" {
		t.Errorf("RegistrationURL: got %q", meta.RegistrationURL)
	}
}

func TestDiscover_pathInsertedASMeta(t *testing.T) {
	// AS URL has a path component — should probe /.well-known/oauth-authorization-server/tenant
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			// PRM returns an AS URL that has the same host but a path
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"authorization_servers": []string{"http://" + r.Host + "/tenant"},
			})
		case "/.well-known/oauth-authorization-server/tenant":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"authorization_endpoint": "https://as.example.com/authorize",
				"token_endpoint":         "https://as.example.com/token",
				"registration_endpoint":  "https://as.example.com/register",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	meta, err := auth.Discover(context.Background(), srv.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if meta.AuthURL != "https://as.example.com/authorize" {
		t.Errorf("AuthURL: got %q", meta.AuthURL)
	}
	if meta.RegistrationURL != "https://as.example.com/register" {
		t.Errorf("RegistrationURL: got %q", meta.RegistrationURL)
	}
}

func TestDiscover_wwwAuthenticateHeader(t *testing.T) {
	// Two-server setup: MCP server returns 401 pointing to a separate AS
	asSrv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint": "https://as.example.com/authorize",
		"token_endpoint":         "https://as.example.com/token",
	})
	defer asSrv.Close()

	prmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/v1/mcp":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"authorization_servers": []string{asSrv.URL},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer prmSrv.Close()

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate",
			`Bearer resource_metadata="`+prmSrv.URL+`/.well-known/oauth-protected-resource/v1/mcp"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	meta, err := auth.Discover(context.Background(), mcpSrv.URL+"/v1/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if meta.AuthURL != "https://as.example.com/authorize" {
		t.Errorf("AuthURL: got %q", meta.AuthURL)
	}
}

func TestDiscover_cimdSupported(t *testing.T) {
	srv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":                  "https://as.example.com/authorize",
		"token_endpoint":                          "https://as.example.com/token",
		"client_id_metadata_document_supported":   true,
	})
	defer srv.Close()

	meta, err := auth.Discover(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.CIMDSupported {
		t.Error("expected CIMDSupported=true")
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
	// RegistrationURL is not guessed in fallback — only populated from real AS metadata
	if meta.RegistrationURL != "" {
		t.Errorf("fallback RegistrationURL: expected empty, got %q", meta.RegistrationURL)
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
	srv := serveASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint": "https://as.example.com/authorize",
		"token_endpoint":         "https://as.example.com/token",
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

func TestDiscover_invalidURL_returnsError(t *testing.T) {
	_, err := auth.Discover(context.Background(), "://bad-url")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestDiscover_malformedWWWAuthenticate_fallsBackGracefully(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"no resource_metadata param", `Bearer realm="example.com"`},
		{"missing closing quote", `Bearer resource_metadata="http://example.com/prm`},
		{"empty value", `Bearer resource_metadata=""`},
		{"no value after prefix", `Bearer resource_metadata=`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Root path returns 401 with malformed header; well-known paths return 404
			// so discovery falls through to fallbackMeta rather than erroring on 401.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("WWW-Authenticate", tc.header)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer srv.Close()

			meta, err := auth.Discover(context.Background(), srv.URL)
			if err != nil {
				t.Fatalf("expected graceful fallback, got error: %v", err)
			}
			if meta == nil {
				t.Fatal("expected non-nil meta from fallback")
			}
		})
	}
}

func TestDiscover_cancelledContext_returnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := auth.Discover(ctx, srv.URL)
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}

func TestDiscover_nonHTTPSchemes_rejected(t *testing.T) {
	auth.UseLoopbackHTTPClient()
	for _, maliciousURL := range []string{"file:///etc/passwd", "ftp://example.com/prm.json"} {
		t.Run(maliciousURL, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+maliciousURL+`"`)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer srv.Close()
			_, err := auth.Discover(context.Background(), srv.URL)
			if err == nil {
				t.Fatalf("Discover with malicious resource_metadata %q succeeded, want error", maliciousURL)
			}
			if !strings.Contains(err.Error(), "unsupported protocol scheme") {
				t.Errorf("expected \"unsupported protocol scheme\" error, got: %v", err)
			}
		})
	}
}
