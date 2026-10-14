//go:build test

package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
)

func TestBrowserLogin_endToEnd(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	token := pkceToken(t, mock.AuthConfig())
	if token.Token.AccessToken != "test-access-token" {
		t.Errorf("access token = %q, want %q", token.Token.AccessToken, "test-access-token")
	}
	if token.Token.RefreshToken != "test-refresh-token" {
		t.Errorf("refresh token = %q, want %q", token.Token.RefreshToken, "test-refresh-token")
	}
	if token.Token.Expiry.IsZero() {
		t.Error("expected non-zero expiry")
	}
}

func TestBrowserLogin_redirectURIUsesLocalhost(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	login := authtest.StartLogin(t, mock.AuthConfig())

	parsed, err := url.Parse(login.AuthURL())
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	redirectURI := parsed.Query().Get("redirect_uri")
	redirectParsed, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("parse redirect_uri: %v", err)
	}
	if redirectParsed.Hostname() != "localhost" {
		t.Errorf("redirect_uri host = %q, want localhost", redirectParsed.Hostname())
	}
}

func TestPKCECallback_emptyCodeRejected(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	login := authtest.StartLogin(t, mock.AuthConfig())

	parsed, _ := url.Parse(login.AuthURL())
	callback := authtest.LoopbackRedirectURI(t, login.AuthURL())
	callback.RawQuery = url.Values{"state": {parsed.Query().Get("state")}}.Encode()

	resp, err := http.Get(callback.String())
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty code, got %d", resp.StatusCode)
	}

	ctx, ctxCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer ctxCancel()
	_, waitErr := login.Wait(ctx)
	if waitErr == nil {
		t.Error("Wait should not succeed after empty code")
	}
}

func TestPKCECallback_stateMismatchRejected(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	login := authtest.StartLogin(t, mock.AuthConfig())

	callback := authtest.LoopbackRedirectURI(t, login.AuthURL())
	callback.RawQuery = url.Values{"code": {"real-code"}, "state": {"wrong-state"}}.Encode()

	resp, err := http.Get(callback.String())
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for state mismatch, got %d", resp.StatusCode)
	}

	ctx, ctxCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer ctxCancel()
	_, waitErr := login.Wait(ctx)
	if waitErr == nil {
		t.Error("Wait should not succeed after state mismatch")
	}
}

func TestPKCECallback_issuerRejectionEndsLoginBeforeRenderingProviderError(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query url.Values
	}{
		{name: "wrong issuer with code", query: url.Values{"code": {"code"}, "iss": {"https://attacker.example"}}},
		{name: "missing required issuer with code", query: url.Values{"code": {"code"}}},
		{name: "wrong issuer with provider error", query: url.Values{"error": {"access_denied"}, "error_description": {"provider-secret-text"}, "iss": {"https://attacker.example"}}},
		{name: "missing required issuer with provider error", query: url.Values{"error": {"access_denied"}, "error_description": {"provider-secret-text"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			login, exchanges := startIssuerBoundLogin(t, issuerBoundLoginTestParams{responseIssSupported: true})
			defer closeIssuerBoundLogin(t, login)
			response := requestIssuerCallback(t, login, tc.query)
			if response.status != http.StatusOK || strings.Contains(response.body, "provider-secret-text") {
				t.Fatalf("callback = %d %q, want fixed secret-safe rejection", response.status, response.body)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := login.Wait(ctx)
			if err == nil || err.Error() != "oauth authorization response issuer rejected" {
				t.Fatalf("Wait error = %v, want fixed issuer rejection", err)
			}
			if got := exchanges.Load(); got != 0 {
				t.Fatalf("token requests = %d, want no code exchange", got)
			}
		})
	}
}

func TestPKCECallback_acceptsMissingIssuerWhenNotAdvertised(t *testing.T) {
	login, exchanges := startIssuerBoundLogin(t, issuerBoundLoginTestParams{})
	defer closeIssuerBoundLogin(t, login)
	response := requestIssuerCallback(t, login, url.Values{"code": {"valid-code"}})
	if response.status != http.StatusOK {
		t.Fatalf(
			"callback = %d %q, want accepted callback without advertised iss support",
			response.status,
			response.body,
		)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := login.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("token requests = %d, want one code exchange", got)
	}
}

func TestPKCECallback_wrongStateDoesNotAbortIssuerValidation(t *testing.T) {
	login, exchanges := startIssuerBoundLogin(t, issuerBoundLoginTestParams{responseIssSupported: true})
	defer closeIssuerBoundLogin(t, login)
	wrongState := requestIssuerCallback(t, login, url.Values{
		"state": {"wrong"}, "iss": {"https://attacker.example"}, "error_description": {"provider-secret-text"},
	})
	if wrongState.status != http.StatusBadRequest || !strings.Contains(wrongState.body, "state mismatch") {
		t.Fatalf("wrong-state response = %d %q, want state mismatch", wrongState.status, wrongState.body)
	}
	valid := requestIssuerCallback(t, login, url.Values{
		"code": {"valid-code"}, "iss": {issuerFromLogin(t, login)},
	})
	if valid.status != http.StatusOK {
		t.Fatalf("valid callback = %d %q", valid.status, valid.body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := login.Wait(ctx); err != nil {
		t.Fatalf("Wait after valid callback: %v", err)
	}
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("token requests = %d, want exactly one valid code exchange", got)
	}
}

func closeIssuerBoundLogin(t *testing.T, login *auth.BrowserLogin) {
	t.Helper()
	if err := login.Close(); err != nil {
		t.Errorf("close login: %v", err)
	}
}

type issuerBoundLoginTestParams struct {
	responseIssSupported bool
	configuredAuthURL    string
}

func TestPKCECallback_ignoresIssuerWhenConfiguredAuthURLOverridesDiscovery(t *testing.T) {
	login, exchanges := startIssuerBoundLogin(t, issuerBoundLoginTestParams{
		responseIssSupported: true, configuredAuthURL: "https://override.example/authorize",
	})
	defer closeIssuerBoundLogin(t, login)
	response := requestIssuerCallback(t, login, url.Values{"code": {"valid-code"}, "iss": {"https://attacker.example"}})
	if response.status != http.StatusOK {
		t.Fatalf("callback = %d %q, want accepted callback without issuer expectation", response.status, response.body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := login.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("token requests = %d, want exchange without issuer expectation", got)
	}
}

func startIssuerBoundLogin(t *testing.T, p issuerBoundLoginTestParams) (*auth.BrowserLogin, *atomic.Int32) {
	t.Helper()
	auth.UseEphemeralCallbackPort()
	auth.UseLoopbackEndpoints()
	t.Cleanup(auth.ResetEndpointValidation)
	var exchanges atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 server.URL,
				"authorization_endpoint": server.URL + "/authorize",
				"token_endpoint":         server.URL + "/token",
				"authorization_response_iss_parameter_supported": p.responseIssSupported,
				"code_challenge_methods_supported":               []string{"S256"},
			})
		case "/token":
			exchanges.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access", "token_type": "Bearer", "expires_in": 3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	login, err := auth.BeginLogin(context.Background(), config.ServerConfig{
		Name: "issuer-bound",
		URL:  server.URL + "/mcp",
		Auth: &config.AuthConfig{
			Type: config.AuthTypeOAuth2, ClientID: "configured-client", AuthURL: p.configuredAuthURL,
		},
	}, auth.BeginLoginParams{ConfigDir: t.TempDir(), ServerName: "issuer-bound"})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	return login, &exchanges
}

func requestIssuerCallback(t *testing.T, login *auth.BrowserLogin, query url.Values) loginCallbackResponse {
	t.Helper()
	parsed, err := url.Parse(login.AuthURL())
	if err != nil {
		t.Fatal(err)
	}
	if query.Get("state") == "" {
		query.Set("state", parsed.Query().Get("state"))
	}
	callback := authtest.LoopbackRedirectURI(t, login.AuthURL())
	callback.RawQuery = query.Encode()
	resp, err := http.Get(callback.String())
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return loginCallbackResponse{status: resp.StatusCode, body: string(body)}
}

func issuerFromLogin(t *testing.T, login *auth.BrowserLogin) string {
	t.Helper()
	parsed, err := url.Parse(login.AuthURL())
	if err != nil {
		t.Fatal(err)
	}
	resource, err := url.Parse(parsed.Query().Get("resource"))
	if err != nil {
		t.Fatal(err)
	}
	return resource.Scheme + "://" + resource.Host
}

func TestPKCECallback_providerErrorEndsLogin(t *testing.T) {
	for _, tc := range []struct {
		name, code, description, authCode, want string
	}{
		{name: "denied", code: "access_denied", want: "access_denied"},
		{name: "errorOverridesCode", code: "access_denied", authCode: "synthetic-code", want: "access_denied"},
		{name: "description", code: "access_denied", description: "User declined", want: "access_denied: User declined"},
		{name: "controls", code: "access\x1b_denied", description: "No\n\r\t\x00thanks", want: "access_denied: Nothanks"},
		{name: "longFields", code: strings.Repeat("x", 300), description: strings.Repeat("y", 300), want: strings.Repeat("x", 256) + ": " + strings.Repeat("y", 256)},
		{name: "unicodeLimit", code: "access_denied", description: strings.Repeat("é", 200), want: "access_denied: " + strings.Repeat("é", 128)},
		{name: "html", code: "access_denied", description: "<script>synthetic</script>", want: "access_denied: <script>synthetic</script>"},
		{name: "emptyError", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			login := authtest.StartLogin(t, &config.AuthConfig{})
			query := url.Values{"error": {tc.code}, "error_description": {tc.description}, "code": {tc.authCode}}
			response := requestLoginCallback(t, login, query)
			want := "oauth authorization failed: " + tc.want
			if response.status != http.StatusOK || !strings.Contains(response.body, html.EscapeString(want)) {
				t.Fatalf("callback = %d %q, want escaped %q", response.status, response.body, want)
			}
			if err := waitForLogin(t, login); err == nil || err.Error() != want {
				t.Fatalf("Wait error = %v, want %q", err, want)
			}
			authtest.RequireCallbackPortReleased(t, login)
		})
	}
}

func TestPKCECallback_wrongStateErrorLeavesLoginUsable(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	login := authtest.StartLogin(t, mock.AuthConfig())
	response := requestLoginCallback(t, login, url.Values{"state": {"wrong-state"}, "error": {"access_denied"}})
	if response.status != http.StatusBadRequest || !strings.Contains(response.body, "state mismatch") {
		t.Fatalf("callback = %d %q, want state mismatch", response.status, response.body)
	}
	authtest.CompleteAuthorization(t, login.AuthURL(), "test-auth-code")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	token, err := login.Wait(ctx)
	if err != nil {
		t.Fatalf("valid callback after wrong state: %v", err)
	}
	if token.Token.AccessToken != "test-access-token" {
		t.Fatalf("access token = %q, want test-access-token", token.Token.AccessToken)
	}
}

func TestBrowserLogin_closeWinsOverBufferedProviderError(t *testing.T) {
	login := authtest.StartLogin(t, &config.AuthConfig{})
	requestLoginCallback(t, login, url.Values{"error": {"access_denied"}})
	if err := login.Close(); err != nil {
		t.Fatal(err)
	}
	if err := waitForLogin(t, login); !errors.Is(err, auth.ErrLoginClosed) {
		t.Fatalf("Wait error after Close = %v, want ErrLoginClosed", err)
	}
}

type loginCallbackResponse struct {
	status int
	body   string
}

func requestLoginCallback(t *testing.T, login *auth.BrowserLogin, query url.Values) loginCallbackResponse {
	t.Helper()
	parsed, err := url.Parse(login.AuthURL())
	if err != nil {
		t.Fatal(err)
	}
	if !query.Has("state") {
		query.Set("state", parsed.Query().Get("state"))
	}
	callback := authtest.LoopbackRedirectURI(t, login.AuthURL())
	callback.RawQuery = query.Encode()
	resp, err := http.Get(callback.String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return loginCallbackResponse{status: resp.StatusCode, body: string(body)}
}

func waitForLogin(t *testing.T, login *auth.BrowserLogin) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := login.Wait(ctx)
	return err
}

func TestLoopbackCallbackPath_consistent(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	login := authtest.StartLogin(t, mock.AuthConfig())

	parsed, _ := url.Parse(login.AuthURL())
	redirectURI := parsed.Query().Get("redirect_uri")
	cbURL, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("parse redirect_uri: %v", err)
	}

	if cbURL.Path != auth.LoopbackCallbackPath {
		t.Errorf(
			"PKCE redirect_uri path = %q, want %q (must match Register's URI)",
			cbURL.Path,
			auth.LoopbackCallbackPath,
		)
	}
}

func TestBrowserLogin_portReleasedAfterWait(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	login := authtest.StartLogin(t, mock.AuthConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	authtest.CompleteAuthorization(t, login.AuthURL(), "test-auth-code")
	if _, err := login.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	authtest.RequireCallbackPortReleased(t, login)
}

func TestBrowserLogin_portReleasedAfterClose(t *testing.T) {
	t.Run("immediate close", func(t *testing.T) {
		login := authtest.StartLogin(t, &config.AuthConfig{})
		login.Close() //nolint:errcheck
		authtest.RequireCallbackPortReleased(t, login)
	})

	t.Run("close with pending wait", func(t *testing.T) {
		login := authtest.StartLogin(t, &config.AuthConfig{})
		waitDone := waitInBackground(login)
		login.Close() //nolint:errcheck
		<-waitDone
		authtest.RequireCallbackPortReleased(t, login)
	})
}

func TestBrowserLogin_closeUnblocksWaitWithErrLoginClosed(t *testing.T) {
	login := authtest.StartLogin(t, &config.AuthConfig{})
	waitDone := waitInBackground(login)

	login.Close() //nolint:errcheck

	if err := <-waitDone; !errors.Is(err, auth.ErrLoginClosed) {
		t.Errorf("Wait returned %v, want ErrLoginClosed", err)
	}
	if _, err := login.Wait(context.Background()); !errors.Is(err, auth.ErrLoginClosed) {
		t.Errorf("second Wait returned %v, want ErrLoginClosed", err)
	}
	login.Close() //nolint:errcheck
}

func TestBrowserLogin_closeWinsOverBufferedCode(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	login := authtest.StartLogin(t, mock.AuthConfig())

	auth.BufferCallbackCode(login, "test-auth-code")
	login.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := login.Wait(ctx); !errors.Is(err, auth.ErrLoginClosed) {
		t.Errorf("Wait returned %v, want ErrLoginClosed", err)
	}
}

func waitInBackground(login *auth.BrowserLogin) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := login.Wait(context.Background())
		done <- err
	}()
	return done
}

func TestBrowserLogin_closeDuringExchangeDiscardsToken(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	exchangeStarted, releaseExchange := make(chan struct{}), make(chan struct{})
	mock.HoldReady, mock.HoldGate = exchangeStarted, releaseExchange
	login := authtest.StartLogin(t, mock.AuthConfig())
	authtest.CompleteAuthorization(t, login.AuthURL(), "test-auth-code")
	waitDone := waitInBackground(login)
	<-exchangeStarted

	login.Close() //nolint:errcheck
	close(releaseExchange)

	if err := <-waitDone; !errors.Is(err, auth.ErrLoginClosed) {
		t.Errorf("Wait returned %v after Close, want ErrLoginClosed", err)
	}
}
