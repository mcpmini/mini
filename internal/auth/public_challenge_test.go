package auth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

type publicChallengeCase struct {
	name, header, scopes string
	status, probes       int
}

func TestPublicLoginDiscoveryFromSavedChallenge(t *testing.T) {
	for _, tc := range []publicChallengeCase{
		{name: "expired token", header: `Bearer error="invalid_token"`, scopes: "read", status: 401, probes: 2},
		{name: "expired token required scopes", header: `Bearer error="invalid_token", scope="write"`, scopes: "write", status: 401, probes: 2},
		{name: "cached metadata", header: `Bearer resource_metadata="https://8.8.8.8/custom-prm", scope="write"`, scopes: "write", status: 401, probes: 0},
		{name: "scope failure", header: `Bearer error="insufficient_scope", scope="write"`, scopes: "write", status: 403, probes: 2},
	} {
		t.Run(tc.name, func(t *testing.T) { checkPublicChallengeCase(t, tc) })
	}
}

func checkPublicChallengeCase(t *testing.T, tc publicChallengeCase) {
	t.Helper()
	fixture := &publicChallengeTransport{status: tc.status, header: tc.header}
	flow := &oauthHTTPFlow{client: &http.Client{Transport: fixture}, clock: clock.System()}
	if flow.origin != nil {
		t.Fatal("fixture must exercise public OAuth policy")
	}
	dir := t.TempDir()
	for range 2 {
		if scopes := checkPublicLoginDiscovery(t, flow, dir); strings.Join(scopes, " ") != tc.scopes {
			t.Fatalf("login scopes = %v, want %q", scopes, tc.scopes)
		}
	}
	if fixture.probes != tc.probes || fixture.tokenRequests != 2 || fixture.wellKnown != 0 {
		t.Fatalf(
			"anonymous/token/well-known requests = %d/%d/%d, want %d/2/0",
			fixture.probes,
			fixture.tokenRequests,
			fixture.wellKnown,
			tc.probes,
		)
	}
}

func checkPublicLoginDiscovery(t *testing.T, flow *oauthHTTPFlow, dir string) []string {
	t.Helper()
	sc := config.ServerConfig{
		Name: "srv",
		URL:  "https://8.8.8.8/mcp",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2},
	}
	recordPublicTokenFailure(t, flow.client, publicTokenFailure{dir: dir, server: sc})
	_, issuer, err := flow.discoverForLogin(context.Background(), &sc, savedChallenge(dir, sc.Name, sc.URL))
	if err != nil || issuer.Issuer != "https://8.8.8.8/custom-as" {
		t.Fatalf("login discovery issuer = %q, err = %v", issuer.Issuer, err)
	}
	return sc.Auth.Scopes
}

type publicTokenFailure struct {
	dir    string
	server config.ServerConfig
}

func recordPublicTokenFailure(t *testing.T, client *http.Client, failure publicTokenFailure) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, failure.server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer expired")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	RecordAuthorizationChallenge(
		failure.dir,
		failure.server,
		Challenge{StatusCode: resp.StatusCode, WWWAuthenticate: resp.Header.Get("WWW-Authenticate")},
	)
	if savedChallenge(failure.dir, failure.server.Name, failure.server.URL) == nil {
		t.Fatal("token-bearing failure did not persist a challenge")
	}
}

type publicChallengeTransport struct {
	status, probes, tokenRequests, wellKnown int
	header                                   string
}

func (f *publicChallengeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: req}
	body := ""
	switch req.URL.Path {
	case "/mcp":
		f.respondToMCP(req, resp)
	case "/custom-prm":
		body = `{"resource":"https://8.8.8.8/mcp","authorization_servers":["https://8.8.8.8/custom-as"]}`
	case "/.well-known/oauth-authorization-server/custom-as":
		body = `{"issuer":"https://8.8.8.8/custom-as","authorization_endpoint":"https://8.8.8.8/authorize","token_endpoint":"https://8.8.8.8/token","code_challenge_methods_supported":["S256"]}`
	default:
		if !strings.HasPrefix(req.URL.Path, "/.well-known/") {
			return nil, fmt.Errorf("unexpected discovery request: %s", req.URL)
		}
		f.wellKnown++
		resp.StatusCode = http.StatusNotFound
	}
	resp.Header.Set("Content-Type", "application/json")
	resp.Body = io.NopCloser(strings.NewReader(body))
	return resp, nil
}

func (f *publicChallengeTransport) respondToMCP(req *http.Request, resp *http.Response) {
	resp.StatusCode = http.StatusUnauthorized
	resp.Header.Set("WWW-Authenticate", `Bearer resource_metadata="https://8.8.8.8/custom-prm", scope="read"`)
	if req.Header.Get("Authorization") != "" {
		f.tokenRequests++
		resp.StatusCode = f.status
		resp.Header.Set("WWW-Authenticate", f.header)
	} else {
		f.probes++
	}
}
