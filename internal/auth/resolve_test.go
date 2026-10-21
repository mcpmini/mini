//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func resolveParams(configDir, serverName string) auth.ResolveEndpointsParams {
	return auth.ResolveEndpointsParams{ConfigDir: configDir, ServerName: serverName, Clock: clock.System()}
}

func TestApplyBearerToken(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		wantHeader string
	}{
		{"default header", "", "Authorization"},
		{"custom header", "X-Api-Key", "X-Api-Key"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sc := &config.ServerConfig{Auth: &config.AuthConfig{Header: tc.header}}
			auth.ApplyBearerToken(sc, "tok123")
			if got := sc.Headers[tc.wantHeader]; got != "Bearer tok123" {
				t.Errorf("Headers[%q] = %q, want %q", tc.wantHeader, got, "Bearer tok123")
			}
		})
	}
}

func TestResolveEndpoints_cimd(t *testing.T) {
	asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":                "https://as.example.com/authorize",
		"token_endpoint":                        "https://as.example.com/token",
		"client_id_metadata_document_supported": true,
		"code_challenge_methods_supported":      []string{"S256"},
	})
	defer asSrv.Close()

	sc := &config.ServerConfig{
		URL:  asSrv.URL + "/mcp",
		Auth: &config.AuthConfig{Type: "oauth2"},
	}
	if _, err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv")); err != nil {
		t.Fatal(err)
	}
	if sc.Auth.ClientID != auth.ClientMetadataURL {
		t.Errorf("ClientID = %q, want ClientMetadataURL", sc.Auth.ClientID)
	}
	if sc.Auth.ResourceURL != sc.URL {
		t.Errorf("ResourceURL = %q, want %q", sc.Auth.ResourceURL, sc.URL)
	}
}

func TestClientMetadataDocumentListsTheRedirectMiniSends(t *testing.T) {
	data := testutil.ReadFile(t, "../../oauth/client-metadata.json")
	var doc struct {
		ClientID     string   `json:"client_id"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ClientID != auth.ClientMetadataURL {
		t.Errorf("client_id = %q, want %q", doc.ClientID, auth.ClientMetadataURL)
	}
	if sent := auth.ResolvedCallbackURI(nil); !slices.Contains(doc.RedirectURIs, sent) {
		t.Errorf("redirect_uris = %v, missing %q", doc.RedirectURIs, sent)
	}
}

func TestResolveEndpoints_refusesAServerWithAnUnsetVariable(t *testing.T) {
	sc := configuredOAuthServer("https://example.com/mcp")
	sc.Auth.ClientSecret = "${MINI_TEST_CLIENT_SECRET}"
	unset := errors.New("server srv: auth.client_secret: MINI_TEST_CLIENT_SECRET isn't set where mini runs")
	sc.UnsetEnv = unset

	_, err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv"))

	if !errors.Is(err, unset) {
		t.Errorf(
			"ResolveEndpoints = %v, want the login refused before it sends the literal ${MINI_TEST_CLIENT_SECRET}",
			err,
		)
	}
}

func TestApplyResourceURL_canonicalizesResourceURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			"userinfo and fragment are stripped",
			"HTTPS://user:secret@EXAMPLE.COM:443/mcp#fragment",
			"https://example.com/mcp",
		},
		{"default HTTP port and root slash are removed", "http://EXAMPLE.COM:80/", "http://example.com"},
		{
			"path and query are preserved",
			"https://EXAMPLE.COM:8443/api/v1?scope=read&mode=full",
			"https://example.com:8443/api/v1?scope=read&mode=full",
		},
		{"IPv6 host is normalized", "https://[2001:DB8::1]:443/mcp?x=1#fragment", "https://[2001:db8::1]/mcp?x=1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sc := configuredOAuthServer(tc.url)
			if err := auth.ApplyResourceURL(sc); err != nil {
				t.Fatalf("ApplyResourceURL: %v", err)
			}
			if sc.Auth.ResourceURL != tc.want {
				t.Errorf("ResourceURL = %q, want %q", sc.Auth.ResourceURL, tc.want)
			}
		})
	}
}

func TestResolveEndpoints_invalidServerURL_returnsError(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "non-http scheme", url: "ftp://example.com/mcp"},
		{name: "relative URL", url: "/mcp"},
		{name: "missing host", url: "https:///mcp"},
		{name: "malformed host", url: "https://[2001:db8::1/mcp"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sc := configuredOAuthServer(tc.url)
			if _, err := auth.ResolveEndpoints(
				context.Background(),
				sc,
				resolveParams(t.TempDir(), "srv"),
			); err == nil {
				t.Fatal("ResolveEndpoints succeeded for invalid resource URL")
			}
		})
	}
}

func configuredOAuthServer(serverURL string) *config.ServerConfig {
	return &config.ServerConfig{
		URL: serverURL,
		Auth: &config.AuthConfig{
			Type:     config.AuthTypeOAuth2,
			AuthURL:  "https://as.example.com/authorize",
			TokenURL: "https://as.example.com/token",
			ClientID: "client-id",
		},
	}
}

func TestResolveEndpoints_cachedRegistrationBeforeCIMD(t *testing.T) {
	asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":                "https://as.example.com/authorize",
		"token_endpoint":                        "https://as.example.com/token",
		"client_id_metadata_document_supported": true,
		"code_challenge_methods_supported":      []string{"S256"},
	})
	defer asSrv.Close()

	dir := t.TempDir()
	authtest.SaveRegistration(t, authtest.RegistrationFile{
		ConfigDir:  dir,
		ServerName: "srv",
		Registration: &auth.Registration{
			ClientID: "cached-id", Issuer: asSrv.URL, TokenURL: "https://as.example.com/token",
		},
	})

	sc := &config.ServerConfig{
		URL:  asSrv.URL + "/mcp",
		Auth: &config.AuthConfig{Type: "oauth2"},
	}
	if _, err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(dir, "srv")); err != nil {
		t.Fatal(err)
	}
	if sc.Auth.ClientID != "cached-id" {
		t.Errorf("ClientID = %q, want cached-id (cached DCR must beat CIMD)", sc.Auth.ClientID)
	}
}

func TestResolveEndpoints_serverWithCIMDAndDCR(t *testing.T) {
	tests := []struct {
		name         string
		callbackPort int
		wantClientID string
	}{
		{name: "default_callback_port_uses_cimd", wantClientID: auth.ClientMetadataURL},
		{name: "custom_callback_port_registers", callbackPort: 7777, wantClientID: "dcr-client-id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var registeredRedirects []string
			regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					RedirectURIs []string `json:"redirect_uris"`
				}
				json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck // an empty request fails the redirect assertion
				registeredRedirects = req.RedirectURIs
				json.NewEncoder(w).Encode(map[string]string{"client_id": "dcr-client-id"}) //nolint:errcheck
			}))
			defer regSrv.Close()
			asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
				"authorization_endpoint":                "https://as.example.com/authorize",
				"token_endpoint":                        "https://as.example.com/token",
				"client_id_metadata_document_supported": true,
				"registration_endpoint":                 regSrv.URL + "/register",
				"code_challenge_methods_supported":      []string{"S256"},
			})
			defer asSrv.Close()
			sc := &config.ServerConfig{
				URL:  asSrv.URL + "/mcp",
				Auth: &config.AuthConfig{Type: "oauth2", CallbackPort: tc.callbackPort},
			}

			if _, err := auth.ResolveEndpoints(
				context.Background(),
				sc,
				resolveParams(t.TempDir(), "srv"),
			); err != nil {
				t.Fatal(err)
			}

			if sc.Auth.ClientID != tc.wantClientID {
				t.Errorf("ClientID = %q, want %q", sc.Auth.ClientID, tc.wantClientID)
			}
			var wantRegistered []string
			if tc.callbackPort != 0 {
				wantRegistered = []string{auth.ResolvedCallbackURI(sc.Auth)}
			}
			if !slices.Equal(registeredRedirects, wantRegistered) {
				t.Errorf("registered redirect URIs = %v, want %v", registeredRedirects, wantRegistered)
			}
		})
	}
}

func TestResolveEndpoints_cimdOnlyServerWithCustomCallbackPortNamesThePort(t *testing.T) {
	asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":                "https://as.example.com/authorize",
		"token_endpoint":                        "https://as.example.com/token",
		"client_id_metadata_document_supported": true,
		"code_challenge_methods_supported":      []string{"S256"},
	})
	defer asSrv.Close()
	sc := &config.ServerConfig{
		URL:  asSrv.URL + "/mcp",
		Auth: &config.AuthConfig{Type: "oauth2", CallbackPort: 7777},
	}

	_, err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv"))

	if err == nil || !strings.Contains(err.Error(), "callback port 6464") {
		t.Errorf("err = %v, want it to name the callback port the metadata document lists", err)
	}
}

func TestResolveEndpoints_rejectsLoopbackDiscoveredEndpoints(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]any
		want string
	}{
		{
			name: "authorization endpoint",
			meta: map[string]any{
				"authorization_endpoint":           "http://127.0.0.1/authorize",
				"token_endpoint":                   "https://as.example.com/token",
				"code_challenge_methods_supported": []string{"S256"},
			},
			want: "authorization_endpoint",
		},
		{
			name: "token endpoint",
			meta: map[string]any{
				"authorization_endpoint":           "https://as.example.com/authorize",
				"token_endpoint":                   "http://127.0.0.1/token",
				"code_challenge_methods_supported": []string{"S256"},
			},
			want: "token_endpoint",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", tc.meta)
			defer asSrv.Close()

			sc := &config.ServerConfig{
				URL:  asSrv.URL + "/mcp",
				Auth: &config.AuthConfig{Type: "oauth2", Issuer: asSrv.URL, ClientID: "pre-configured"},
			}
			_, err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv"))
			if err == nil {
				t.Fatal("expected loopback endpoint validation error")
			}
			if got := err.Error(); got == "" || !strings.Contains(got, tc.want) {
				t.Fatalf("error = %q, want mention of %s", got, tc.want)
			}
		})
	}
}

func TestResolveEndpoints_scopesAutoPopulatedFromPRM(t *testing.T) {
	asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
	})
	defer asSrv.Close()

	prmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"authorization_servers": []string{asSrv.URL},
			"scopes_supported":      []string{"channels:read", "chat:write"},
		})
	}))
	defer prmSrv.Close()

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().
			Set("WWW-Authenticate", `Bearer resource_metadata="`+prmSrv.URL+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	sc := &config.ServerConfig{
		URL:  mcpSrv.URL + "/mcp",
		Auth: &config.AuthConfig{Type: "oauth2", Issuer: asSrv.URL, ClientID: "pre-configured"},
	}
	if _, err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv")); err != nil {
		t.Fatal(err)
	}
	if len(sc.Auth.Scopes) != 2 || sc.Auth.Scopes[0] != "channels:read" {
		t.Errorf("Scopes = %v, want [channels:read chat:write]", sc.Auth.Scopes)
	}
}

func TestResolveEndpoints_userScopesNotOverwritten(t *testing.T) {
	asSrv := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":           "https://as.example.com/authorize",
		"token_endpoint":                   "https://as.example.com/token",
		"code_challenge_methods_supported": []string{"S256"},
		"scopes_supported":                 []string{"channels:read"},
	})
	defer asSrv.Close()

	sc := &config.ServerConfig{
		URL: asSrv.URL + "/mcp",
		Auth: &config.AuthConfig{
			Type:     "oauth2",
			Issuer:   asSrv.URL,
			ClientID: "pre-configured",
			Scopes:   []string{"custom:scope"},
		},
	}
	if _, err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv")); err != nil {
		t.Fatal(err)
	}
	if len(sc.Auth.Scopes) != 1 || sc.Auth.Scopes[0] != "custom:scope" {
		t.Errorf("Scopes = %v, want user-configured [custom:scope] preserved", sc.Auth.Scopes)
	}
}

func TestValidateOAuthServer(t *testing.T) {
	tests := []struct {
		name    string
		sc      config.ServerConfig
		wantErr bool
	}{
		{"no auth", config.ServerConfig{Name: "s"}, true},
		{"apikey auth", config.ServerConfig{Name: "s", Auth: &config.AuthConfig{Type: "apikey"}}, true},
		{"oauth2 auth", config.ServerConfig{Name: "s", Auth: &config.AuthConfig{Type: "oauth2"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := auth.ValidateOAuthServer("s", tc.sc)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateOAuthServer() error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}
