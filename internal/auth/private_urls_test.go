//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

type privateOAuthOptions struct {
	RedirectRegistration, RedirectToken bool
	AuthURL, TokenURL                   string
}
type privateOAuthRequests struct{ tokens, targets, requests atomic.Int32 }

func privateOAuthServer(t *testing.T, opts privateOAuthOptions) (*httptest.Server, *privateOAuthRequests) {
	t.Helper()
	counts := &privateOAuthRequests{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := server.URL
		counts.requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{base}})
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           base,
				"authorization_endpoint":           privateEndpoint(opts.AuthURL, base+"/authorize"),
				"token_endpoint":                   privateEndpoint(opts.TokenURL, base+"/token"),
				"registration_endpoint":            base + "/register",
				"code_challenge_methods_supported": []string{"S256"},
			})
		case "/register":
			if opts.RedirectRegistration {
				http.Redirect(w, r, "/target", http.StatusFound)
				return
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"client_id":"registered-client"}`))
		case "/token":
			counts.tokens.Add(1)
			if opts.RedirectToken {
				http.Redirect(w, r, "/target", http.StatusFound)
				return
			}
			w.Write([]byte(`{"access_token":"local-token","refresh_token":"local-refresh","token_type":"Bearer"}`))
		case "/target":
			counts.targets.Add(1)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, counts
}

func productionOAuthPolicy(t *testing.T) {
	t.Helper()
	auth.UseProductionHTTPClient()
	auth.ResetEndpointValidation()
	t.Cleanup(auth.UseLoopbackHTTPClient)
	t.Cleanup(auth.ResetEndpointValidation)
}

func localOAuthConfig(serverURL string) config.ServerConfig {
	return config.ServerConfig{URL: serverURL + "/mcp", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2}}
}

func TestPrivateOAuthProductionFlow(t *testing.T) {
	productionOAuthPolicy(t)
	auth.UseEphemeralCallbackPort()
	server, counts := privateOAuthServer(t, privateOAuthOptions{})
	sc := localOAuthConfig(server.URL)
	login, err := auth.BeginLogin(context.Background(), sc, resolveParams(t.TempDir(), "local"))
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close()
	u, err := url.Parse(login.AuthURL())
	if err != nil {
		t.Fatal(err)
	}
	callback := u.Query().Get("redirect_uri") + "?code=local-code&state=" + url.QueryEscape(u.Query().Get("state"))
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	saved, err := login.Wait(context.Background())
	if err != nil || saved.Token.AccessToken != "local-token" {
		t.Fatalf("exchange = %v, %v", saved, err)
	}
	saved.Token = &oauth2.Token{RefreshToken: saved.Token.RefreshToken}
	refreshed, err := auth.Refresh(context.Background(), auth.RefreshParams{Server: sc, Login: saved})
	if err != nil || refreshed.AccessToken != "local-token" {
		t.Fatalf("refresh = %v, %v", refreshed, err)
	}
	if counts.tokens.Load() != 2 {
		t.Fatalf("token requests = %d", counts.tokens.Load())
	}
	for _, changed := range []config.ServerConfig{
		{URL: "https://8.8.8.8/mcp", Auth: sc.Auth}, {URL: sc.URL, Auth: sc.Auth, AgentAdded: true},
	} {
		if _, err := auth.Refresh(
			context.Background(),
			auth.RefreshParams{Server: changed, Login: saved},
		); err == nil {
			t.Fatal("changed provenance allowed private refresh")
		}
	}
	if counts.tokens.Load() != 2 {
		t.Fatal("denied refresh reached target")
	}
}

func TestPrivateOAuthPolicyIsolation(t *testing.T) {
	productionOAuthPolicy(t)
	server, _ := privateOAuthServer(t, privateOAuthOptions{})
	strict := make(chan error, 1)
	private := make(chan error, 1)
	go func() { _, err := auth.Discover(context.Background(), server.URL+"/mcp"); strict <- err }()
	go func() {
		sc := localOAuthConfig(server.URL)
		_, err := auth.ResolveEndpoints(context.Background(), &sc, resolveParams(t.TempDir(), "local"))
		private <- err
	}()
	if err := <-strict; err == nil {
		t.Fatal("strict discovery unexpectedly succeeded")
	}
	if err := <-private; err != nil {
		t.Fatalf("private discovery failed: %v", err)
	}
}

func TestPrivateOAuthRedirectsDenied(t *testing.T) {
	productionOAuthPolicy(t)
	t.Run("registration", func(t *testing.T) {
		server, counts := privateOAuthServer(t, privateOAuthOptions{RedirectRegistration: true})
		sc := localOAuthConfig(server.URL)
		if _, err := auth.ResolveEndpoints(context.Background(), &sc, resolveParams(t.TempDir(), "local")); err == nil {
			t.Fatal("registration followed redirect")
		}
		if counts.targets.Load() != 0 {
			t.Fatal("registration redirect reached target")
		}
	})
	t.Run("token", func(t *testing.T) {
		server, counts := privateOAuthServer(t, privateOAuthOptions{RedirectToken: true})
		sc := localOAuthConfig(server.URL)
		sc.Auth.TokenURL = server.URL + "/token"
		sc.Auth.Issuer = server.URL
		if _, err := auth.Refresh(
			context.Background(),
			refreshParams(t, sc, &oauth2.Token{RefreshToken: "refresh"}),
		); err == nil {
			t.Fatal("refresh followed redirect")
		}
		if counts.targets.Load() != 0 {
			t.Fatal("token redirect reached target")
		}
	})
}

func TestPrivateOAuthRejectsNonHTTPEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name, authURL, tokenURL string
		configured              bool
	}{
		{name: "configured authorization", authURL: "ftp://localhost/authorize", configured: true},
		{name: "configured token", tokenURL: "file:///token", configured: true},
		{name: "discovered authorization", authURL: "ftp://localhost/authorize"},
		{name: "discovered token", tokenURL: "file:///token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := privateOAuthOptions{}
			if !tc.configured {
				opts.AuthURL, opts.TokenURL = tc.authURL, tc.tokenURL
			}
			server, _ := privateOAuthServer(t, opts)
			sc := localOAuthConfig(server.URL)
			if tc.configured {
				sc.Auth.AuthURL, sc.Auth.TokenURL = tc.authURL, tc.tokenURL
			}
			_, err := auth.ResolveEndpoints(context.Background(), &sc, resolveParams(t.TempDir(), "local"))
			if err == nil || !strings.Contains(err.Error(), "absolute HTTP(S)") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestPrivateOAuthAgentDiscoveryDenied(t *testing.T) {
	productionOAuthPolicy(t)
	server, counts := privateOAuthServer(t, privateOAuthOptions{})
	sc := localOAuthConfig(server.URL)
	sc.AgentAdded = true
	if _, err := auth.ResolveEndpoints(context.Background(), &sc, resolveParams(t.TempDir(), "local")); err == nil {
		t.Fatal("agent private discovery succeeded")
	}
	if counts.requests.Load() != 0 {
		t.Fatal("agent reached private server")
	}
}

func TestPrivateOAuthHeaderlessDiscovery(t *testing.T) {
	productionOAuthPolicy(t)
	server, _ := privateOAuthServer(t, privateOAuthOptions{})
	if !auth.RequiresOAuth(context.Background(), config.ServerConfig{URL: server.URL + "/mcp"}, "") {
		t.Fatal("headerless private PRM not detected")
	}
}

func privateEndpoint(configured, fallback string) string {
	if configured != "" {
		return configured
	}
	return fallback
}
