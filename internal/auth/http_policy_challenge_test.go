//go:build test

package auth

import (
	"context"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

func TestPrivateLoginReacquiresRoutingBeforeUsingSavedChallenge(t *testing.T) {
	f := newPinnedOAuthFixture(t)
	installRebindingNetwork(t, f)
	dir := t.TempDir()
	sc := config.ServerConfig{URL: f.origin + "/mcp", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2}}
	if err := saveChallenge(
		dir,
		"srv",
		Challenge{
			ServerURL:       sc.URL,
			StatusCode:      401,
			WWWAuthenticate: `Bearer resource_metadata="http://127.0.0.1:1/obsolete", scope="write"`,
		},
	); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake()
	resolved, err := ResolveEndpoints(
		context.Background(),
		&sc,
		ResolveEndpointsParams{ConfigDir: dir, ServerName: "srv", Clock: clk},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Issuer.Issuer != f.origin || resolved.flow.clock != clk || f.source.Load() != 1 ||
		f.public.Load() != 0 {
		t.Fatalf("issuer/source/public = %q/%d/%d", resolved.Issuer.Issuer, f.source.Load(), f.public.Load())
	}
	if len(sc.Auth.Scopes) != 1 || sc.Auth.Scopes[0] != "write" {
		t.Fatalf("saved required scopes lost: %v", sc.Auth.Scopes)
	}
}
