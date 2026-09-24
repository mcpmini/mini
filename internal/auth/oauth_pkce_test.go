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

func TestStartPKCEFlow_redirectURIContract(t *testing.T) {
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
	cbURL, err := url.Parse(parsed.Query().Get("redirect_uri"))
	if err != nil {
		t.Fatalf("parse redirect_uri: %v", err)
	}
	if cbURL.Hostname() != "localhost" {
		t.Errorf("redirect_uri host = %q, want localhost", cbURL.Hostname())
	}
	if cbURL.Path != auth.LoopbackCallbackPath {
		t.Errorf("redirect_uri path = %q, want %q (must match Register's URI)", cbURL.Path, auth.LoopbackCallbackPath)
	}
	cancel()
	<-doneCh
}

func TestPKCECallback_invalidRequestRejected(t *testing.T) {
	tests := []struct {
		name     string
		buildURL func(redirectURI, state string) string
	}{
		{
			name: "empty code",
			buildURL: func(redirectURI, state string) string {
				return redirectURI + "?state=" + url.QueryEscape(state)
			},
		},
		{
			name: "state mismatch",
			buildURL: func(redirectURI, _ string) string {
				return redirectURI + "?code=real-code&state=wrong-state"
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertCallbackRejected(t, tc.buildURL)
		})
	}
}

func assertCallbackRejected(t *testing.T, buildURL func(redirectURI, state string) string) {
	t.Helper()
	mock := newMockAuthServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	authURL, doneCh, err := auth.StartPKCEFlow(ctx, mock.authConfig())
	if err != nil {
		t.Fatalf("StartPKCEFlow: %v", err)
	}
	parsed, _ := url.Parse(authURL)
	state := parsed.Query().Get("state")
	redirectURI := parsed.Query().Get("redirect_uri")
	resp, err := http.Get(buildURL(redirectURI, state))
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
	select {
	case result := <-doneCh:
		t.Errorf("expected no result, got: %v", result)
	default:
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
