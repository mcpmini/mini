//go:build test

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config/configtest"
)

func TestRunListUsesStoredOAuthLogin(t *testing.T) {
	dir := t.TempDir()
	upstream := newTestMCPUpstream(t)
	tokens := newTestTokenEndpoint(t)
	sc := oauthServerConfig("live", upstream.srv.URL, tokens.srv.URL, true)
	configtest.WriteServer(t, dir, sc)
	authtest.SaveToken(t, authtest.TokenFile{
		ConfigDir: dir, ServerName: sc.Name, Server: sc,
		Token: &oauth2.Token{AccessToken: "stored-access", Expiry: time.Now().Add(time.Hour)},
	})
	var out bytes.Buffer
	if err := runList(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	if got := upstream.lastAuthFor("tools/list"); got != "Bearer stored-access" {
		t.Errorf("tool discovery authorization = %q, want saved login", got)
	}
	if got := strings.Join(strings.Fields(out.String()), " "); !strings.Contains(got, "live http ready") {
		t.Errorf("list does not show the authenticated server as ready: %s", out.String())
	}
	if tokens.hits.Load() != 0 {
		t.Error("valid stored login unexpectedly triggered a token exchange")
	}
}
