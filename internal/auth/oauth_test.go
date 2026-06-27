package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

// mockAuthServer is a minimal OAuth2 server for testing.
type mockAuthServer struct {
	srv          *httptest.Server
	accessToken  string
	refreshToken string
	refreshed    bool
}

func newMockAuthServer(t *testing.T) *mockAuthServer {
	t.Helper()
	m := &mockAuthServer{
		accessToken:  "test-access-token",
		refreshToken: "test-refresh-token",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", m.handleToken)
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockAuthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.FormValue("grant_type") == "refresh_token" {
		m.refreshed = true
		m.accessToken = "refreshed-access-token"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token":  m.accessToken,
		"refresh_token": m.refreshToken,
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

func (m *mockAuthServer) authConfig() *config.AuthConfig {
	return &config.AuthConfig{
		Type:     "oauth2",
		ClientID: "test-client-id",
		AuthURL:  m.srv.URL + "/authorize", // doesn't need to exist; we skip it
		TokenURL: m.srv.URL + "/token",
	}
}

// simulateBrowser parses the auth URL that PKCEFlow generates, then fires the
// callback asynchronously so PKCEFlow's select can receive the code.
// This avoids the deadlock where openBrowser blocks waiting for the callback
// server to respond while PKCEFlow is also inside openBrowser.
func simulateBrowser(authURL string) error {
	parsed, err := url.Parse(authURL)
	if err != nil {
		return err
	}
	q := parsed.Query()
	state := q.Get("state")
	redirectURI := q.Get("redirect_uri")

	callbackURL := redirectURI + "?code=test-auth-code&state=" + url.QueryEscape(state)
	go http.Get(callbackURL) //nolint:errcheck
	return nil
}

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

func pkceToken(t *testing.T, mock *mockAuthServer) *oauth2.Token {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	token, err := auth.PKCEFlow(ctx, mock.authConfig(), simulateBrowser)
	if err != nil {
		t.Fatalf("PKCEFlow: %v", err)
	}
	return token
}

func TestRefresh(t *testing.T) {
	mock := newMockAuthServer(t)
	dir := t.TempDir()
	token := pkceToken(t, mock)
	if err := auth.Save(dir, "srv", token); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, _ := auth.Load(dir, "srv")
	loaded.Expiry = time.Now().Add(-time.Hour)
	mock.accessToken = "refreshed-access-token"
	newTok, err := auth.Refresh(context.Background(), mock.authConfig(), loaded)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !mock.refreshed {
		t.Error("expected /token to be called with grant_type=refresh_token")
	}
	if newTok.AccessToken != "refreshed-access-token" {
		t.Errorf("access token = %q, want %q", newTok.AccessToken, "refreshed-access-token")
	}
}

func TestTokenSaveLoad(t *testing.T) {
	mock := newMockAuthServer(t)
	dir := t.TempDir()
	token := pkceToken(t, mock)
	if err := auth.Save(dir, "myserver", token); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := auth.Load(dir, "myserver")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccessToken != token.AccessToken {
		t.Errorf("loaded token = %q, want %q", loaded.AccessToken, token.AccessToken)
	}
	if !loaded.Valid() {
		t.Error("loaded token should be valid")
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

func TestIsNotFound(t *testing.T) {
	_, err := auth.Load(t.TempDir(), "nonexistent")
	if !auth.IsNotFound(err) {
		t.Errorf("expected IsNotFound=true for missing token, got false (err: %v)", err)
	}
}

func TestSave_tokenFilePermissions(t *testing.T) {
	mock := newMockAuthServer(t)
	dir := t.TempDir()
	if err := auth.Save(dir, "myserver", pkceToken(t, mock)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertTokenFilesPrivate(t, dir+"/internal")
}

func assertTokenFilesPrivate(t *testing.T, tokensDir string) {
	t.Helper()
	entries, err := os.ReadDir(tokensDir)
	if err != nil {
		t.Fatalf("ReadDir tokens: %v", err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("Stat %s: %v", e.Name(), err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Errorf("token file %s has world/group-readable permissions: %v", e.Name(), info.Mode().Perm())
		}
	}
}

func TestSave_invalidServerName(t *testing.T) {
	token := &oauth2.Token{AccessToken: "tok"}
	err := auth.Save(t.TempDir(), "bad name!", token)
	if err == nil {
		t.Error("expected error for invalid server name")
	}
}

func TestLoad_invalidServerName(t *testing.T) {
	_, err := auth.Load(t.TempDir(), "bad name!")
	if err == nil {
		t.Error("expected error for invalid server name")
	}
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

func TestTokenValidAfterForcedExpiry(t *testing.T) {
	mock := newMockAuthServer(t)
	dir := t.TempDir()
	if err := auth.Save(dir, "srv", pkceToken(t, mock)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, _ := auth.Load(dir, "srv")
	loaded.Expiry = time.Now().Add(-time.Hour)
	auth.Save(dir, "srv", loaded) //nolint:errcheck
	reloaded, _ := auth.Load(dir, "srv")
	if reloaded.Valid() {
		t.Error("token should be invalid after forced expiry")
	}
}
