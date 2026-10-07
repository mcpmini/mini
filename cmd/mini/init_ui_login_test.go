package main

import (
	"context"
	"testing"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func oauthServerIn(t *testing.T, tokenServer *authtest.TokenServer) string {
	t.Helper()
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name: "svc", Transport: "http", URL: tokenServer.Srv.URL + "/mcp", Auth: tokenServer.AuthConfig(),
	})
	return dir
}

func browserThat(t *testing.T, open func(authURL string)) {
	t.Helper()
	orig := openBrowser
	openBrowser = func(authURL string) error {
		open(authURL)
		return nil
	}
	t.Cleanup(func() { openBrowser = orig })
}

func TestStartInitLogin_aCompletedLoginSavesTheToken(t *testing.T) {
	tokenServer := authtest.NewTokenServer(t)
	dir := oauthServerIn(t, tokenServer)
	browserThat(t, func(authURL string) { go authtest.CompleteAuthorization(t, authURL, "test-auth-code") })

	login, err := startInitLogin(dir)(t.Context(), "svc")
	if err != nil || login.URL == "" {
		t.Fatalf("start = %+v, %v; want a login with its URL", login, err)
	}
	if err := login.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if _, err := auth.Load(dir, "svc"); err != nil {
		t.Errorf("token after the login: %v, want it saved", err)
	}
}

func TestStartInitLogin_aCancelledLoginSavesNothing(t *testing.T) {
	tokenServer := authtest.NewTokenServer(t)
	dir := oauthServerIn(t, tokenServer)
	browserThat(t, func(string) {})
	ctx, cancel := context.WithCancel(t.Context())

	login, err := startInitLogin(dir)(ctx, "svc")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := login.Wait(); err == nil {
		t.Error("Wait after cancel returned nil, want the cancellation")
	}
	if _, err := auth.Load(dir, "svc"); !auth.IsNotFound(err) {
		t.Errorf("token after a cancelled login: %v, want none", err)
	}
}

func TestStartInitLogin_aServerWithoutOAuthIsAnError(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "local", Command: "run"})
	if _, err := startInitLogin(dir)(t.Context(), "local"); err == nil {
		t.Error("start on a stdio server returned no error")
	}
}
