package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

type pinnedOAuthFixture struct {
	skipPRM                                        bool
	origin                                         string
	source, metadata, registration, tokens, public atomic.Int32
	issuerMismatch                                 atomic.Bool
	resourceMismatch                               atomic.Bool
	mismatch                                       atomic.Bool
}

func newPinnedOAuthFixture(t *testing.T) *pinnedOAuthFixture {
	t.Helper()
	fixture := &pinnedOAuthFixture{}
	server := httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(server.Close)
	u, _ := url.Parse(server.URL)
	fixture.origin = "http://configured.corp:" + u.Port()
	return fixture
}

func (f *pinnedOAuthFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Connection", "close")
	switch r.URL.Path {
	case "/mcp":
		f.source.Add(1)
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	case "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource":
		f.metadata.Add(1)
		if f.skipPRM {
			http.NotFound(w, r)
			return
		}
		resource := f.origin + "/mcp"
		if f.resourceMismatch.Load() {
			resource = f.origin
		}
		json.NewEncoder(w).Encode(map[string]any{"resource": resource, "authorization_servers": []string{f.origin}})
	case "/.well-known/oauth-authorization-server":
		f.metadata.Add(1)
		token := f.origin + "/token"
		if f.mismatch.Load() {
			token = f.origin + "/other-token"
		}
		issuer := f.origin
		if f.issuerMismatch.Load() {
			issuer = f.origin + "/changed"
		}
		json.NewEncoder(w).
			Encode(map[string]any{"issuer": issuer, "authorization_endpoint": f.origin + "/authorize", "token_endpoint": token, "registration_endpoint": f.origin + "/register", "code_challenge_methods_supported": []string{"S256"}})
	case "/register":
		f.registration.Add(1)
		w.Write([]byte(`{"client_id":"pinned-client"}`))
	case "/token", "/other-token":
		f.tokens.Add(1)
		w.Write([]byte(`{"access_token":"pinned-token","refresh_token":"pinned-refresh","token_type":"Bearer"}`))
	default:
		http.NotFound(w, r)
	}
}

func installRebindingNetwork(t *testing.T, f *pinnedOAuthFixture) *atomic.Int32 {
	t.Helper()
	var lookups atomic.Int32
	originalLookup, originalTransport, originalClient := lookupAuthHost, http.DefaultTransport, noRedirectClient
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.public.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(public.Close)
	lookupAuthHost = func(context.Context, string) ([]net.IPAddr, error) {
		if lookups.Add(1) > 1 {
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	changed := http.DefaultTransport.(*http.Transport).Clone()
	publicURL, _ := url.Parse(public.URL)
	changed.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, publicURL.Host)
	}
	http.DefaultTransport = changed
	noRedirectClient = newAuthHTTPClient()
	t.Cleanup(func() {
		lookupAuthHost, http.DefaultTransport, noRedirectClient = originalLookup, originalTransport, originalClient
	})
	return &lookups
}

func TestPrivateOAuthPinsDNSAcrossDiscoveryAndExchange(t *testing.T) {
	t.Run("PRM fallback", func(t *testing.T) { testPinnedOAuthExchange(t, false) })
	t.Run("AS fallback", func(t *testing.T) { testPinnedOAuthExchange(t, true) })
}

func testPinnedOAuthExchange(t *testing.T, skipPRM bool) {
	t.Helper()
	f := newPinnedOAuthFixture(t)
	f.skipPRM = skipPRM
	lookups := installRebindingNetwork(t, f)
	sc := config.ServerConfig{URL: f.origin + "/mcp", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2}}
	originalListen := callbackListenAddr
	callbackListenAddr = func(*config.AuthConfig) string { return "127.0.0.1:0" }
	t.Cleanup(func() { callbackListenAddr = originalListen })
	login, err := BeginLogin(
		context.Background(),
		sc,
		BeginLoginParams{ConfigDir: t.TempDir(), ServerName: "pinned", Clock: clock.System()},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Close()
	BufferCallbackCode(login, "pinned-code")
	saved, err := login.Wait(context.Background())
	if err != nil || saved.Token.AccessToken != "pinned-token" {
		t.Fatalf("exchange = %v, %v", saved, err)
	}
	wantMetadata := int32(2)
	if skipPRM {
		wantMetadata = 3
	}
	if lookups.Load() != 1 || f.source.Load() != 1 || f.metadata.Load() != wantMetadata || f.registration.Load() != 1 ||
		f.tokens.Load() != 1 ||
		f.public.Load() != 0 {
		t.Fatalf(
			"lookups/source/metadata/registration/token/public = %d/%d/%d/%d/%d/%d",
			lookups.Load(),
			f.source.Load(),
			f.metadata.Load(),
			f.registration.Load(),
			f.tokens.Load(),
			f.public.Load(),
		)
	}
}

func TestPrivateRefreshFreshBinding(t *testing.T) {
	for _, mismatch := range []string{"none", "token", "issuer", "configured override", "resource", "resource with configured override"} {
		t.Run(mismatch, func(t *testing.T) {
			f := newPinnedOAuthFixture(t)
			f.mismatch.Store(mismatch == "token" || mismatch == "configured override")
			f.issuerMismatch.Store(mismatch == "issuer")
			f.resourceMismatch.Store(mismatch == "resource" || mismatch == "resource with configured override")
			lookups := installRebindingNetwork(t, f)
			sc := config.ServerConfig{
				URL:  f.origin + "/mcp",
				Auth: &config.AuthConfig{Issuer: f.origin, TokenURL: f.origin + "/token", ClientID: "saved-client"},
			}
			login := &Login{
				Resource:  sc.URL,
				ServerURL: sc.URL,
				Issuer:    f.origin,
				TokenURL:  f.origin + "/token",
				Client:    LoginClient{ID: "saved-client", Configured: true},
				Token:     &oauth2.Token{RefreshToken: "saved-refresh"},
			}
			if mismatch != "configured override" && mismatch != "resource with configured override" {
				sc.Auth.TokenURL = ""
			}
			token, err := Refresh(context.Background(), RefreshParams{Server: sc, Login: login})
			if mismatch != "none" && mismatch != "configured override" {
				if !errors.Is(err, ErrLoginMismatch) || f.tokens.Load() != 0 {
					t.Fatalf("mismatch refresh = %v, %v; posts=%d", token, err, f.tokens.Load())
				}
			} else if err != nil || token.AccessToken != "pinned-token" || f.tokens.Load() != 1 {
				t.Fatalf("refresh = %v, %v; posts=%d", token, err, f.tokens.Load())
			}
			if lookups.Load() != 1 || f.source.Load() != 1 || f.metadata.Load() != 2 || f.public.Load() != 0 {
				t.Fatalf(
					"fresh pinned refresh lookup/source/metadata/public = %d/%d/%d/%d",
					lookups.Load(),
					f.source.Load(),
					f.metadata.Load(),
					f.public.Load(),
				)
			}
		})
	}
}

func TestMixedDNSDoesNotReachPrivateSource(t *testing.T) {
	f := newPinnedOAuthFixture(t)
	installRebindingNetwork(t, f)
	lookupAuthHost = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("8.8.8.8")}}, nil
	}
	sc := config.ServerConfig{URL: f.origin + "/mcp", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2}}
	if _, err := resolveEndpoints(
		context.Background(),
		&sc,
		BeginLoginParams{Clock: clock.System()},
	); err == nil {
		t.Fatal("mixed DNS discovery succeeded")
	}
	if f.source.Load() != 0 || f.tokens.Load() != 0 {
		t.Fatal("mixed DNS reached private source")
	}
}
