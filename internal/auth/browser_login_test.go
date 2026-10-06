//go:build test

package auth_test

import (
	"context"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
)

func TestBrowserLogin_endToEnd(t *testing.T) {
	mock := authtest.NewTokenServer(t)
	token := pkceToken(t, mock.AuthConfig())
	if token.AccessToken != "test-access-token" {
		t.Errorf("access token = %q, want %q", token.AccessToken, "test-access-token")
	}
	if token.RefreshToken != "test-refresh-token" {
		t.Errorf("refresh token = %q, want %q", token.RefreshToken, "test-refresh-token")
	}
	if token.Expiry.IsZero() {
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
	if token.AccessToken != "test-access-token" {
		t.Fatalf("access token = %q, want test-access-token", token.AccessToken)
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
