package auth_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

func TestNeedsAuthorization(t *testing.T) {
	dir := t.TempDir()
	oauthServer := config.ServerConfig{Name: "oauth", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2}}
	for _, tc := range []struct {
		name     string
		server   config.ServerConfig
		token    *oauth2.Token
		corrupt  bool
		wantNeed bool
		wantNote string
	}{
		{name: "non oauth server", server: config.ServerConfig{Name: "plain"}},
		{name: "missing token", server: oauthServer, wantNeed: true},
		{name: "valid token", server: oauthServer, token: &oauth2.Token{AccessToken: "token"}},
		{name: "expired token with refresh token", server: oauthServer, token: &oauth2.Token{AccessToken: "token", RefreshToken: "refresh", Expiry: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}},
		{name: "expired token without refresh token", server: oauthServer, token: &oauth2.Token{AccessToken: "token", Expiry: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}, wantNeed: true},
		{name: "corrupt token", server: oauthServer, corrupt: true, wantNeed: true, wantNote: "token unreadable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := tc.server
			server.Name += "_" + strings.ReplaceAll(tc.name, " ", "_")
			if tc.token != nil {
				if err := auth.Save(dir, server.Name, tc.token); err != nil {
					t.Fatal(err)
				}
			}
			if tc.corrupt {
				path := filepath.Join(dir, "internal", server.Name+".token.json")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			need := auth.NeedsAuthorization(dir, server)
			if need.Needed != tc.wantNeed {
				t.Errorf("Needed = %v, want %v", need.Needed, tc.wantNeed)
			}
			if !strings.Contains(need.Note, tc.wantNote) {
				t.Errorf("Note = %q, want substring %q", need.Note, tc.wantNote)
			}
		})
	}
}
