package auth_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/testutil"
	"golang.org/x/oauth2"
)

func TestReadTokenState(t *testing.T) {
	expired := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		token   *oauth2.Token
		corrupt bool
		want    auth.TokenState
	}{
		{name: "missing file", want: auth.TokenMissing},
		{name: "valid", token: &oauth2.Token{AccessToken: "t", Expiry: time.Now().Add(time.Hour)}, want: auth.TokenValid},
		{name: "valid with zero expiry", token: &oauth2.Token{AccessToken: "t"}, want: auth.TokenValid},
		{name: "expired with refresh token", token: &oauth2.Token{AccessToken: "t", RefreshToken: "r", Expiry: expired}, want: auth.TokenRefreshable},
		{name: "expired without refresh token", token: &oauth2.Token{AccessToken: "t", Expiry: expired}, want: auth.TokenExpired},
		{name: "corrupt file", corrupt: true, want: auth.TokenUnreadable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.token != nil {
				if err := auth.Save(dir, "srv", tc.token); err != nil {
					t.Fatal(err)
				}
			}
			if tc.corrupt {
				writeCorruptToken(t, dir, "srv")
			}
			got, err := auth.ReadTokenState(dir, "srv")
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

func TestTokenStateStringsAreDistinct(t *testing.T) {
	seen := map[string]auth.TokenState{}
	for _, state := range []auth.TokenState{auth.TokenMissing, auth.TokenUnreadable, auth.TokenExpired, auth.TokenRefreshable, auth.TokenValid} {
		text := state.String()
		if text == "unknown token state" {
			t.Errorf("%d has no description", state)
		}
		if prev, dup := seen[text]; dup {
			t.Errorf("%v and %v share description %q", prev, state, text)
		}
		seen[text] = state
	}
}

func writeCorruptToken(t *testing.T, dir, serverName string) {
	t.Helper()
	path := filepath.Join(dir, "internal", serverName+".token.json")
	testutil.WriteFile(t, path, "not json")
}
