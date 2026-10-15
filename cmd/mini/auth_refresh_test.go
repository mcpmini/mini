//go:build test

package main

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
)

func TestInjectToken_expiredToken_refreshSendsCanonicalResource(t *testing.T) {
	tokenEp := newTestTokenEndpoint(t)

	dir := t.TempDir()
	sc := &config.ServerConfig{
		Name: "srv",
		Auth: &config.AuthConfig{
			Type:     config.AuthTypeOAuth2,
			ClientID: "client",
			TokenURL: tokenEp.srv.URL,
		},
		URL: "HTTPS://Example.COM:443/mcp",
	}
	expired := &oauth2.Token{
		AccessToken:  "expired",
		RefreshToken: "old-refresh",
		Expiry:       time.Now().Add(-time.Hour),
	}
	authtest.SaveToken(t, authtest.TokenFile{ConfigDir: dir, ServerName: "srv", Server: *sc, Token: expired})

	injectToken(context.Background(), dir, sc)

	const wantResource = "https://example.com/mcp"
	if got, _ := tokenEp.resourceValue.Load().(string); got != wantResource {
		t.Errorf("resource = %q, want %q", got, wantResource)
	}
	if sc.Headers["Authorization"] == "" {
		t.Error("expected Authorization header to be set after refresh")
	}
}

func TestInjectToken_loginForAnotherServerURL_isNotSentOrRefreshed(t *testing.T) {
	tokenEp := newTestTokenEndpoint(t)
	dir := t.TempDir()
	sc := &config.ServerConfig{
		Name: "srv",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "client", TokenURL: tokenEp.srv.URL},
		URL:  "https://moved.example.com/mcp",
	}
	madeFor := *sc
	madeFor.URL = "https://example.com/mcp"
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir: dir, ServerName: "srv", Server: madeFor,
		Token: &oauth2.Token{AccessToken: "old", RefreshToken: "r", Expiry: time.Now().Add(-time.Hour)},
	})

	injectToken(context.Background(), dir, sc)

	if got := sc.Headers["Authorization"]; got != "" {
		t.Errorf("Authorization = %q, want none for a login made for another URL", got)
	}
	if hits := tokenEp.hits.Load(); hits != 0 {
		t.Errorf("token endpoint hits = %d, want 0", hits)
	}
}
