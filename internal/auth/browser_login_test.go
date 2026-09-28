//go:build test

package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
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
		t.Errorf("PKCE redirect_uri path = %q, want %q (must match Register's URI)", cbURL.Path, auth.LoopbackCallbackPath)
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

	auth.LoginCodeCh(login) <- "test-auth-code"
	login.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := login.Wait(ctx); !errors.Is(err, auth.ErrLoginClosed) {
		t.Errorf("Wait returned %v, want ErrLoginClosed", err)
	}
	if hits := mock.Hits.Load(); hits != 0 {
		t.Errorf("token server was called %d times, want 0", hits)
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
