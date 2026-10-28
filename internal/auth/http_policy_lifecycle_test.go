package auth

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func idleTrackingSource(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mcp":
			w.WriteHeader(http.StatusUnauthorized)
		case "/.well-known/oauth-authorization-server":
			origin := "http://" + r.Host
			w.Write(
				[]byte(
					`{"issuer":"` + origin + `","authorization_endpoint":"` + origin + `/authorize","token_endpoint":"` + origin + `/token","code_challenge_methods_supported":["S256"]}`,
				),
			)
		case "/token":
			w.Write([]byte(`{"access_token":"fresh","token_type":"Bearer"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			connections.Add(1)
		}
		if s == http.StateClosed {
			connections.Add(-1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	return server.URL, &connections
}

func assertConnectionsClosed(t *testing.T, connections *atomic.Int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for connections.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := connections.Load(); got != 0 {
		t.Fatalf("owned idle connections remain: %d", got)
	}
}

func TestOAuthDiscoveryClosesOwnedIdleConnections(t *testing.T) {
	origin, connections := idleTrackingSource(t)
	if _, err := DiscoverServer(t.Context(), config.ServerConfig{URL: origin + "/mcp"}); err != nil {
		t.Fatal(err)
	}
	assertConnectionsClosed(t, connections)
}

func TestOAuthFailedResolutionClosesOwnedIdleConnections(t *testing.T) {
	origin, connections := idleTrackingSource(t)
	sc := config.ServerConfig{
		URL:  origin + "/mcp",
		Auth: &config.AuthConfig{ClientID: "configured", Issuer: "https://other.example"},
	}
	if _, err := ResolveEndpoints(t.Context(), &sc, ResolveEndpointsParams{Clock: clock.System()}); err == nil {
		t.Fatal("issuer mismatch accepted")
	}
	assertConnectionsClosed(t, connections)
}

func TestBrowserLoginOwnsResolvedIdleConnections(t *testing.T) {
	for _, finish := range []string{"close", "wait"} {
		t.Run(finish, func(t *testing.T) {
			origin, connections := idleTrackingSource(t)
			sc := config.ServerConfig{
				URL:  origin + "/mcp",
				Auth: &config.AuthConfig{ClientID: "configured", Issuer: origin},
			}
			resolved, err := ResolveEndpoints(t.Context(), &sc, ResolveEndpointsParams{Clock: clock.System()})
			if err != nil {
				t.Fatal(err)
			}
			if connections.Load() == 0 {
				t.Fatal("resolution closed flow before browser login")
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			login, err := startBrowserLogin(sc.Auth, listener, resolved)
			if err != nil {
				t.Fatal(err)
			}
			defer login.Close()
			if finish == "wait" {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := login.Wait(ctx); err == nil {
					t.Fatal("cancelled wait succeeded")
				}
			} else {
				login.Close()
			}
			assertConnectionsClosed(t, connections)
		})
	}
}

func TestOAuthRefreshClosesOwnedIdleConnections(t *testing.T) {
	origin, connections := idleTrackingSource(t)
	sc := config.ServerConfig{URL: origin + "/mcp", Auth: &config.AuthConfig{ClientID: "configured"}}
	login := &Login{
		Resource: sc.URL,
		Issuer:   origin,
		TokenURL: origin + "/token",
		Client:   LoginClient{ID: "configured", Configured: true},
		Token:    &oauth2.Token{RefreshToken: "refresh"},
	}
	token, err := Refresh(t.Context(), RefreshParams{Server: sc, Login: login})
	if err != nil || token.AccessToken != "fresh" {
		t.Fatalf("refresh=%v err=%v", token, err)
	}
	assertConnectionsClosed(t, connections)
}

func TestOAuthDetectionClosesOwnedIdleConnections(t *testing.T) {
	origin, connections := idleTrackingSource(t)
	if RequiresOAuth(t.Context(), config.ServerConfig{URL: origin + "/mcp"}, "") {
		t.Fatal("missing PRM accepted")
	}
	assertConnectionsClosed(t, connections)
}

func TestOAuthFlowDoesNotCloseSharedStrictPool(t *testing.T) {
	origin, connections := idleTrackingSource(t)
	original := noRedirectClient
	shared := &http.Transport{}
	noRedirectClient = &http.Client{Transport: shared}
	t.Cleanup(func() { noRedirectClient = original; shared.CloseIdleConnections() })
	flow := newOAuthHTTPFlow(t.Context(), config.ServerConfig{URL: origin, AgentAdded: true})
	resp, err := flow.doDiscoveryRequest(t.Context(), origin+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	flow.closeIdleConnections()
	if connections.Load() != 1 {
		t.Fatalf("shared connection closed: %d", connections.Load())
	}
	shared.CloseIdleConnections()
	assertConnectionsClosed(t, connections)
}
