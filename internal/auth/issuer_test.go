//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
)

const honestIssuer = "https://as.example.com"

func TestBrowserLogin_issInTheResponse_isCheckedBeforeTheCodeIsExchanged(t *testing.T) {
	for _, tc := range []struct {
		name    string
		expect  auth.ExpectedIssuer
		iss     []string
		wantErr string
	}{
		{name: "advertised and matching", expect: auth.ExpectedIssuer{Issuer: honestIssuer, AlwaysSent: true}, iss: []string{honestIssuer}},
		{name: "advertised and missing", expect: auth.ExpectedIssuer{Issuer: honestIssuer, AlwaysSent: true}, wantErr: "missing iss"},
		{name: "not advertised but present and matching", expect: auth.ExpectedIssuer{Issuer: honestIssuer}, iss: []string{honestIssuer}},
		{name: "not advertised and missing", expect: auth.ExpectedIssuer{Issuer: honestIssuer}},
		{name: "another issuer", expect: auth.ExpectedIssuer{Issuer: honestIssuer}, iss: []string{"https://attacker.example"}, wantErr: `"https://attacker.example"`},
		{name: "not normalized", expect: auth.ExpectedIssuer{Issuer: honestIssuer}, iss: []string{honestIssuer + "/"}, wantErr: "came from issuer"},
		{name: "no validated metadata", iss: []string{"https://anything.example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokens := authtest.NewTokenServer(t)
			login := startLoginExpecting(t, tokens, tc.expect)

			requestLoginCallback(t, login, url.Values{"code": {"synthetic-code"}, "iss": tc.iss})
			err := waitForLogin(t, login)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Wait = %v, want the login to complete", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Wait = %v, want an error containing %s", err, tc.wantErr)
			}
			if hits := tokens.Hits.Load(); hits != 0 {
				t.Errorf("token endpoint hits = %d, want 0: the code must not leave mini", hits)
			}
		})
	}
}

func startLoginExpecting(t *testing.T, tokens *authtest.TokenServer, expect auth.ExpectedIssuer) *auth.BrowserLogin {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ac := tokens.AuthConfig()
	resolved := auth.ResolvedLogin{Client: auth.LoginClient{ID: ac.ClientID, Configured: true}, Issuer: expect}
	login, err := auth.StartBrowserLogin(ac, listener, resolved)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { login.Close() }) //nolint:errcheck // Close always returns nil
	return login
}

func TestDiscover_metadataNamingAnotherIssuer_isRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		issuer func(asURL string) string
	}{
		{name: "another server", issuer: func(string) string { return honestIssuer }},
		{name: "trailing slash", issuer: func(asURL string) string { return asURL + "/" }},
		{name: "missing", issuer: func(string) string { return "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/.well-known/oauth-authorization-server" {
					http.NotFound(w, r)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
					"issuer":                           tc.issuer("http://" + r.Host),
					"authorization_endpoint":           "https://as.example.com/authorize",
					"token_endpoint":                   "https://as.example.com/token",
					"code_challenge_methods_supported": []string{"S256"},
				})
			}))
			t.Cleanup(as.Close)

			_, err := auth.Discover(context.Background(), as.URL)

			if err == nil || !strings.Contains(err.Error(), "names issuer") {
				t.Fatalf("Discover = %v, want the metadata refused for its issuer", err)
			}
		})
	}
}

func TestResolveEndpoints_expectsTheIssuerOnlyWhenItsMetadataChoseTheAuthorizationEndpoint(t *testing.T) {
	as := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":                         "https://as.example.com/authorize",
		"token_endpoint":                                 "https://as.example.com/token",
		"code_challenge_methods_supported":               []string{"S256"},
		"authorization_response_iss_parameter_supported": true,
	})
	t.Cleanup(as.Close)
	for _, tc := range []struct {
		name    string
		authURL string
		want    auth.ExpectedIssuer
	}{
		{name: "discovered", want: auth.ExpectedIssuer{Issuer: as.URL, AlwaysSent: true}},
		{name: "configured", authURL: "https://other.example.com/authorize"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := &config.ServerConfig{URL: as.URL + "/mcp", Auth: &config.AuthConfig{
				Type: "oauth2", ClientID: "configured-client", AuthURL: tc.authURL,
			}}

			resolved, err := auth.ResolveEndpoints(context.Background(), sc, resolveParams(t.TempDir(), "srv"))
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Issuer != tc.want {
				t.Errorf("expected issuer = %+v, want %+v", resolved.Issuer, tc.want)
			}
		})
	}
}

func TestBeginLogin_callbackFromAnotherIssuer_neverReachesTheTokenEndpoint(t *testing.T) {
	auth.UseEphemeralCallbackPort()
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)
	tokens := authtest.NewTokenServer(t)
	as := authtest.ServeASMeta(t, "/.well-known/oauth-authorization-server", map[string]any{
		"authorization_endpoint":                         tokens.Srv.URL + "/authorize",
		"token_endpoint":                                 tokens.Srv.URL + "/token",
		"code_challenge_methods_supported":               []string{"S256"},
		"authorization_response_iss_parameter_supported": true,
	})
	t.Cleanup(as.Close)
	sc := config.ServerConfig{Name: "synthetic", URL: as.URL + "/mcp", Auth: &config.AuthConfig{
		Type: "oauth2", ClientID: "configured-client",
	}}
	login, err := auth.BeginLogin(context.Background(), sc, beginLoginParams(t))
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	t.Cleanup(func() { login.Close() }) //nolint:errcheck // Close always returns nil

	requestLoginCallback(t, login, url.Values{"code": {"synthetic-code"}, "iss": {"https://attacker.example"}})

	if err := waitForLogin(t, login); err == nil || !strings.Contains(err.Error(), "came from issuer") {
		t.Fatalf("Wait = %v, want the response refused for its issuer", err)
	}
	if hits := tokens.Hits.Load(); hits != 0 {
		t.Errorf("token endpoint hits = %d, want 0", hits)
	}
}
