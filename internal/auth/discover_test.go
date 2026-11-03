//go:build test

package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestDiscover_rootASMeta(t *testing.T) {
	srv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"registration_endpoint":            "https://as.example.com/register",
		"code_challenge_methods_supported": []string{"S256"},
	})

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
	srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			// PRM returns an AS URL that has the same host but a path
			testutil.WriteJSON(w, http.StatusOK, map[string]any{
				"resource":              "http://" + r.Host + "/mcp",
				"authorization_servers": []string{"http://" + r.Host + "/tenant"},
			})
		case "/.well-known/oauth-authorization-server/tenant":
			testutil.WriteJSON(w, http.StatusOK, map[string]any{
				"issuer":                           "http://" + r.Host + "/tenant",
				"authorization_endpoint":           "https://as.example.com/authorize",
				"token_endpoint":                   "https://as.example.com/token",
				"registration_endpoint":            "https://as.example.com/register",
				"code_challenge_methods_supported": []string{"S256"},
			})
		default:
			http.NotFound(w, r)
		}
	})

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
	asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})

	prmPath := "/.well-known/oauth-protected-resource/v1/mcp"
	prm := &authtest.ProtectedResource{AuthorizationServers: []string{asSrv.URL}}
	prmSrv := authtest.ServeProtectedResource(t, prmPath, prm)
	mcpSrv := authtest.ServeChallenge(t, authtest.Challenge{ResourceMetadata: prmSrv.URL + prmPath})
	prm.Resource = mcpSrv.URL + "/v1/mcp"

	meta, err := auth.Discover(context.Background(), mcpSrv.URL+"/v1/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if meta.AuthURL != "https://as.example.com/authorize" {
		t.Errorf("AuthURL: got %q", meta.AuthURL)
	}
}

func TestDiscover_cimdSupported(t *testing.T) {
	srv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":                "https://as.example.com/authorize",
		"token_endpoint":                        "https://as.example.com/token",
		"client_id_metadata_document_supported": true,
		"code_challenge_methods_supported":      []string{"S256"},
	})

	meta, err := auth.Discover(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.CIMDSupported {
		t.Error("expected CIMDSupported=true")
	}
}

func TestDiscover_404_fallsBack(t *testing.T) {
	srv := testutil.Serve(t, http.NotFound)

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

func TestDiscover_noPKCE_returnsError(t *testing.T) {
	srv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint": "https://as.example.com/authorize",
		"token_endpoint":         "https://as.example.com/token",
	})

	_, err := auth.Discover(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error when code_challenge_methods_supported absent")
	}
}

func TestDiscover_serverError_returnsError(t *testing.T) {
	srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	})

	_, err := auth.Discover(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error for 500 response, got nil")
	}
}

func TestDiscover_noPathURL(t *testing.T) {
	srv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})

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
			srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("WWW-Authenticate", tc.header)
				w.WriteHeader(http.StatusUnauthorized)
			})

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

	srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, err := auth.Discover(ctx, srv.URL)
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}

func TestDiscover_scopesFromPRM(t *testing.T) {
	asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})

	prm := &authtest.ProtectedResource{
		AuthorizationServers: []string{asSrv.URL},
		ScopesSupported:      []string{"channels:read", "chat:write"},
	}
	prmSrv := authtest.ServeProtectedResource(t, "/.well-known/oauth-protected-resource", prm)
	mcpSrv := authtest.ServeChallenge(t, authtest.Challenge{
		ResourceMetadata: prmSrv.URL + "/.well-known/oauth-protected-resource",
	})
	prm.Resource = mcpSrv.URL

	meta, err := auth.Discover(context.Background(), mcpSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Scopes) != 2 || meta.Scopes[0] != "channels:read" || meta.Scopes[1] != "chat:write" {
		t.Errorf("Scopes from PRM: got %v, want [channels:read chat:write]", meta.Scopes)
	}
}

func TestDiscover_wwwAuthScopePreservedWhenPRMHasNoAS(t *testing.T) {
	asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})

	emptyPRM := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteJSON(w, http.StatusOK, map[string]any{})
	})

	var mcpSrv *httptest.Server
	mcpSrv = testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			testutil.WriteJSON(w, http.StatusOK, map[string]any{
				"resource":              mcpSrv.URL,
				"authorization_servers": []string{asSrv.URL},
			})
			return
		}
		authtest.Challenge{
			ResourceMetadata: emptyPRM.URL + "/.well-known/oauth-protected-resource",
			Scope:            "files:read",
		}.Write(w)
	})

	meta, err := auth.Discover(context.Background(), mcpSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Scopes) != 1 || meta.Scopes[0] != "files:read" {
		t.Errorf(
			"Scopes: got %v, want [files:read] (WWW-Authenticate scope must survive PRM-no-AS fallback)",
			meta.Scopes,
		)
	}
}

func TestDiscover_scopesFromPRMProbePath(t *testing.T) {
	// When the server returns no 401, discovery falls straight to the PRM probe path.
	// Scopes from that PRM's scopes_supported must propagate to meta.Scopes.
	asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})

	prm := &authtest.ProtectedResource{
		AuthorizationServers: []string{asSrv.URL},
		ScopesSupported:      []string{"read", "write"},
	}
	mcpSrv := authtest.ServeProtectedResource(t, "/.well-known/oauth-protected-resource", prm)
	prm.Resource = mcpSrv.URL

	meta, err := auth.Discover(context.Background(), mcpSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Scopes) != 2 || meta.Scopes[0] != "read" || meta.Scopes[1] != "write" {
		t.Errorf("Scopes from PRM probe: got %v, want [read write]", meta.Scopes)
	}
}

func TestDiscover_noScopesWhenASMetaOnly(t *testing.T) {
	srv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"scopes_supported":                 []string{"openid", "profile", "email"},
		"code_challenge_methods_supported": []string{"S256"},
	})

	meta, err := auth.Discover(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Scopes) != 0 {
		t.Errorf(
			"Scopes: got %v, want empty (AS metadata scopes_supported must not be used for scope selection)",
			meta.Scopes,
		)
	}
}

func TestDiscover_scopeFromWWWAuthenticateBeforesPRM(t *testing.T) {
	asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})

	prm := &authtest.ProtectedResource{
		AuthorizationServers: []string{asSrv.URL},
		ScopesSupported:      []string{"channels:read", "chat:write"},
	}
	prmSrv := authtest.ServeProtectedResource(t, "/.well-known/oauth-protected-resource", prm)

	// scope in WWW-Authenticate header takes priority over PRM scopes_supported
	mcpSrv := authtest.ServeChallenge(t, authtest.Challenge{
		ResourceMetadata: prmSrv.URL + "/.well-known/oauth-protected-resource",
		Scope:            "files:read",
	})
	prm.Resource = mcpSrv.URL

	meta, err := auth.Discover(context.Background(), mcpSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Scopes) != 1 || meta.Scopes[0] != "files:read" {
		t.Errorf("Scopes: got %v, want [files:read] (WWW-Authenticate scope should beat PRM)", meta.Scopes)
	}
}

func TestDiscover_scopeFromWWWAuthenticateWithoutResourceMetadata(t *testing.T) {
	srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			testutil.WriteJSON(w, http.StatusOK, map[string]any{
				"issuer":                           "http://" + r.Host,
				"authorization_endpoint":           "https://as.example.com/authorize",
				"token_endpoint":                   "https://as.example.com/token",
				"code_challenge_methods_supported": []string{"S256"},
			})
		default:
			w.Header().Set("WWW-Authenticate", `Bearer scope="files:read"`)
			w.WriteHeader(http.StatusUnauthorized)
		}
	})

	meta, err := auth.Discover(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Scopes) != 1 || meta.Scopes[0] != "files:read" {
		t.Errorf("Scopes: got %v, want [files:read]", meta.Scopes)
	}
}

func TestRequiresOAuth_bearerChallengeShortCircuitsWithoutNetworkCall(t *testing.T) {
	called := false
	srv := testutil.Serve(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.NotFound(w, r)
	})

	if !auth.RequiresOAuth(context.Background(), config.ServerConfig{URL: srv.URL + "/mcp"}, `Bearer realm="mcp"`) {
		t.Fatal("expected true for a Bearer challenge")
	}
	if called {
		t.Error("expected no network call when header already confirms OAuth")
	}
}

func TestRequiresOAuth_nonBearerSchemeIsNotOAuth(t *testing.T) {
	srv := testutil.Serve(t, http.NotFound)

	if auth.RequiresOAuth(context.Background(), config.ServerConfig{URL: srv.URL}, `Basic realm="internal"`) {
		t.Fatal("expected false for a Basic challenge — it's a legitimate non-OAuth auth mechanism")
	}
}

func TestRequiresOAuth_nonBearerSchemeSkipsPRMFallback(t *testing.T) {
	prm := &authtest.ProtectedResource{AuthorizationServers: []string{"https://as.example.com"}}
	srv := authtest.ServeProtectedResource(t, "/.well-known/oauth-protected-resource", prm)
	prm.Resource = srv.URL

	if auth.RequiresOAuth(context.Background(), config.ServerConfig{URL: srv.URL}, `Basic realm="internal"`) {
		t.Fatal(
			"expected false — a Basic challenge is decisive even if a PRM document coincidentally exists at the same origin",
		)
	}
}

func TestRequiresOAuth_prmDocumentConfirmsOAuth(t *testing.T) {
	prm := &authtest.ProtectedResource{AuthorizationServers: []string{"https://as.example.com"}}
	srv := authtest.ServeProtectedResource(t, "/.well-known/oauth-protected-resource", prm)
	prm.Resource = srv.URL

	if !auth.RequiresOAuth(context.Background(), config.ServerConfig{URL: srv.URL}, "") {
		t.Fatal("expected true when a PRM document exists")
	}
}

func TestRequiresOAuth_noHeaderNoPRM_returnsFalse(t *testing.T) {
	srv := testutil.Serve(t, http.NotFound)

	if auth.RequiresOAuth(context.Background(), config.ServerConfig{URL: srv.URL}, "") {
		t.Fatal("expected false when neither header nor PRM document confirms OAuth")
	}
}

func TestDiscover_nonHTTPSchemes_rejected(t *testing.T) {
	auth.UseLoopbackHTTPClient()
	for _, maliciousURL := range []string{"file:///etc/passwd", "ftp://example.com/prm.json"} {
		t.Run(maliciousURL, func(t *testing.T) {
			srv := authtest.ServeChallenge(t, authtest.Challenge{ResourceMetadata: maliciousURL})
			_, err := auth.Discover(context.Background(), srv.URL)
			testutil.ErrorContains(t, err, "unsupported protocol scheme")
		})
	}
}
