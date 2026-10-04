//go:build test

package main

import (
	"context"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"golang.org/x/oauth2"
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
	authtest.SaveToken(t, authtest.TokenFile{ConfigDir: dir, ServerName: "srv", Token: expired})

	injectToken(context.Background(), dir, sc)

	const wantResource = "https://example.com/mcp"
	if got, _ := tokenEp.resourceValue.Load().(string); got != wantResource {
		t.Errorf("resource = %q, want %q", got, wantResource)
	}
	if sc.Headers["Authorization"] == "" {
		t.Error("expected Authorization header to be set after refresh")
	}
}
