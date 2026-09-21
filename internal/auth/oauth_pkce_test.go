package auth_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
)

func TestPKCEFlowEndToEnd(t *testing.T) {
	mock := newMockAuthServer(t)
	token := pkceToken(t, mock)
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

func TestStartPKCEFlow_nonBlocking(t *testing.T) {
	mock := newMockAuthServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	authURL, doneCh, err := auth.StartPKCEFlow(ctx, mock.authConfig())
	if err != nil {
		t.Fatalf("StartPKCEFlow: %v", err)
	}
	if authURL == "" {
		t.Fatal("expected non-empty auth URL")
	}
	// Simulate browser completing the flow.
	simulateBrowser(authURL) //nolint:errcheck
	result := <-doneCh
	if result.Err != nil {
		t.Fatalf("StartPKCEFlow result error: %v", result.Err)
	}
	if result.Token.AccessToken != "test-access-token" {
		t.Errorf("access token = %q, want %q", result.Token.AccessToken, "test-access-token")
	}
}

func TestStartPKCEFlow_redirectURIUsesLocalhost(t *testing.T) {
	mock := newMockAuthServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	authURL, doneCh, err := auth.StartPKCEFlow(ctx, mock.authConfig())
	if err != nil {
		t.Fatalf("StartPKCEFlow: %v", err)
	}
	parsed, err := url.Parse(authURL)
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
	// Drain channel so test doesn't leak.
	cancel()
	<-doneCh
}

// TestPKCECallback_emptyCodeRejected verifies that the OAuth callback handler
// returns 400 when the auth code is absent, rather than writing "Authorized"
// and sending an empty string to the token exchange.
func TestPKCECallback_emptyCodeRejected(t *testing.T) {
	mock := newMockAuthServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	authURL, doneCh, err := auth.StartPKCEFlow(ctx, mock.authConfig())
	if err != nil {
		t.Fatalf("StartPKCEFlow: %v", err)
	}

	// Parse the callback URL, then hit it without a code param.
	parsed, _ := url.Parse(authURL)
	state := parsed.Query().Get("state")
	redirectURI := parsed.Query().Get("redirect_uri")

	resp, err := http.Get(redirectURI + "?state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty code, got %d", resp.StatusCode)
	}

	// doneCh must not have received a result (flow is still waiting for a valid code).
	select {
	case result := <-doneCh:
		t.Errorf("expected no result yet, got: %v", result)
	default:
	}

	cancel() // clean up the goroutine
	<-doneCh // drain
}

// TestPKCECallback_stateMismatchRejected verifies that a mismatched state
// returns 400 and does not deliver a code.
func TestPKCECallback_stateMismatchRejected(t *testing.T) {
	mock := newMockAuthServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	authURL, doneCh, err := auth.StartPKCEFlow(ctx, mock.authConfig())
	if err != nil {
		t.Fatalf("StartPKCEFlow: %v", err)
	}
	parsed, _ := url.Parse(authURL)
	redirectURI := parsed.Query().Get("redirect_uri")

	resp, err := http.Get(redirectURI + "?code=real-code&state=wrong-state")
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for state mismatch, got %d", resp.StatusCode)
	}

	select {
	case result := <-doneCh:
		t.Errorf("expected no result on state mismatch, got: %v", result)
	default:
	}

	cancel()
	<-doneCh
}

// TestLoopbackCallbackPath_consistent verifies that the path registered during
// dynamic client registration matches the path used in the actual PKCE flow.
// This guards against the two sides diverging (e.g. /callback vs /cb).
func TestLoopbackCallbackPath_consistent(t *testing.T) {
	mock := newMockAuthServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	authURL, doneCh, err := auth.StartPKCEFlow(ctx, mock.authConfig())
	if err != nil {
		t.Fatalf("StartPKCEFlow: %v", err)
	}

	parsed, _ := url.Parse(authURL)
	redirectURI := parsed.Query().Get("redirect_uri")
	cbURL, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("parse redirect_uri: %v", err)
	}

	if cbURL.Path != auth.LoopbackCallbackPath {
		t.Errorf("PKCE redirect_uri path = %q, want %q (must match Register's URI)", cbURL.Path, auth.LoopbackCallbackPath)
	}

	cancel()
	<-doneCh
}

func TestBuildAuthURL_extraParamsDoNotOverrideResource(t *testing.T) {
	mock := newMockAuthServer(t)
	ac := mock.authConfig()
	ac.ResourceURL = "https://resource.example.com"
	ac.ExtraAuthParams = map[string]string{
		"resource": "https://evil.example.com", // must not win
		"prompt":   "consent",                  // must pass through
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	authURL, doneCh, err := auth.StartPKCEFlow(ctx, ac)
	if err != nil {
		t.Fatalf("StartPKCEFlow: %v", err)
	}
	defer func() { cancel(); <-doneCh }()

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	q := parsed.Query()
	if got := q.Get("resource"); got != "https://resource.example.com" {
		t.Errorf("resource = %q, want %q — ExtraAuthParams must not override computed resource", got, "https://resource.example.com")
	}
	if got := q.Get("prompt"); got != "consent" {
		t.Errorf("prompt = %q, want consent — ExtraAuthParams should pass through", got)
	}
}
