package auth_test

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestReadTokenState(t *testing.T) {
	expired := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		token    *oauth2.Token
		savedFor string
		file     string
		want     auth.TokenState
	}{
		{name: "missing file", want: auth.TokenMissing},
		{name: "valid", token: &oauth2.Token{AccessToken: "t", Expiry: time.Now().Add(time.Hour)}, want: auth.TokenValid},
		{name: "valid with zero expiry", token: &oauth2.Token{AccessToken: "t"}, want: auth.TokenValid},
		{name: "expired with refresh token", token: &oauth2.Token{AccessToken: "t", RefreshToken: "r", Expiry: expired}, want: auth.TokenRefreshable},
		{name: "expired without refresh token", token: &oauth2.Token{AccessToken: "t", Expiry: expired}, want: auth.TokenExpired},
		{name: "corrupt file", file: "not json", want: auth.TokenUnreadable},
		{name: "bare token from an older mini", file: `{"access_token":"t"}`, want: auth.TokenUnreadable},
		{
			name:     "login for another server URL",
			token:    &oauth2.Token{AccessToken: "t"},
			savedFor: "https://old.example.com/mcp",
			want:     auth.TokenUnreadable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.token != nil {
				server := config.ServerConfig{URL: tc.savedFor}
				authtest.SaveToken(
					t,
					authtest.TokenFile{ConfigDir: dir, ServerName: "srv", Server: server, Token: tc.token},
				)
			}
			if tc.file != "" {
				testutil.WriteFile(t, filepath.Join(dir, "internal", "srv.token.json"), tc.file)
			}
			got, err := auth.ReadTokenState(dir, config.ServerConfig{Name: "srv", URL: authtest.DefaultServerURL})
			if got != tc.want {
				t.Errorf("state = %v, want %v", got, tc.want)
			}
			if (err != nil) != (tc.want == auth.TokenUnreadable) {
				t.Errorf("err = %v for state %v", err, tc.want)
			}
		})
	}
}

func TestTokenStateNeedsLogin(t *testing.T) {
	for state, want := range map[auth.TokenState]bool{
		auth.TokenMissing:     true,
		auth.TokenUnreadable:  true,
		auth.TokenExpired:     true,
		auth.TokenRefreshable: false,
		auth.TokenValid:       false,
	} {
		if got := state.NeedsLogin(); got != want {
			t.Errorf("%v.NeedsLogin() = %v, want %v", state, got, want)
		}
	}
}
