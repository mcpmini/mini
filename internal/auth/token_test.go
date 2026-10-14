package auth_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestReadLoginState(t *testing.T) {
	expired := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		token   *oauth2.Token
		corrupt bool
		legacy  bool
		want    auth.LoginState
	}{
		{name: "missing", want: auth.Missing},
		{name: "valid", token: &oauth2.Token{AccessToken: "t", Expiry: time.Now().Add(time.Hour)}, want: auth.Valid},
		{name: "valid with zero expiry", token: &oauth2.Token{AccessToken: "t"}, want: auth.Valid},
		{name: "expired with refresh token", token: &oauth2.Token{AccessToken: "t", RefreshToken: "r", Expiry: expired}, want: auth.Refreshable},
		{name: "expired without refresh token", token: &oauth2.Token{AccessToken: "t", Expiry: expired}, want: auth.Expired},
		{name: "corrupt file", corrupt: true, want: auth.Unreadable},
		{name: "old raw token", legacy: true, want: auth.Unreadable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.token != nil {
				authtest.SaveToken(
					t,
					authtest.TokenFile{
						ConfigDir:  dir,
						ServerName: "srv",
						Resource:   "https://srv.example.com",
						TokenURL:   "https://issuer.example.com/token",
						Client:     auth.Client{Source: auth.ClientConfigured, ID: "client"},
						Token:      tc.token,
					},
				)
			}
			if tc.corrupt {
				testutil.WriteFile(t, filepath.Join(dir, "internal", "srv.token.json"), "not json")
			}
			if tc.legacy {
				testutil.WriteFile(t, filepath.Join(dir, "internal", "srv.token.json"), `{"access_token":"old"}`)
			}
			state, err := auth.ReadLoginState(dir, "srv", loginServerConfig())
			if state != tc.want {
				t.Errorf("state = %v, want %v", state, tc.want)
			}
			if (err != nil) != (tc.want == auth.Unreadable) {
				t.Errorf("err = %v for state %v", err, tc.want)
			}
		})
	}
}

func TestLoginEligibilityRejectsChangedSettings(t *testing.T) {
	login := &auth.Login{
		Resource: "https://srv.example.com",
		TokenURL: "https://issuer.example.com/token",
		Client:   auth.Client{Source: auth.ClientConfigured, ID: "client"},
		Token:    &oauth2.Token{AccessToken: "secret"},
	}
	matching := config.ServerConfig{
		Name: "srv",
		URL:  "https://srv.example.com",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "client"},
	}
	if eligible, reason := auth.LoginEligible(login, matching); !eligible {
		t.Fatalf("matching login rejected: %s", reason)
	}
	for _, sc := range []config.ServerConfig{
		{Name: "srv", URL: "https://other.example.com", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "client"}},
		{Name: "srv", URL: "https://srv.example.com", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "other"}},
		{Name: "srv", URL: "https://srv.example.com", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "client", TokenURL: "https://changed.example.com/token"}},
		{Name: "srv", URL: "https://srv.example.com", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "client"}, UnsetEnv: errors.New("unset env")},
	} {
		if eligible, _ := auth.LoginEligible(login, sc); eligible {
			t.Errorf("login accepted for settings %+v", sc)
		}
	}
	registered := *login
	registered.Client = auth.Client{Source: auth.ClientRegistered, ID: login.Client.ID}
	if eligible, _ := auth.LoginEligible(&registered, matching); eligible {
		t.Fatal("registered login accepted after a configured client appeared")
	}
	withoutConfiguredClient := matching
	withoutConfiguredClient.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2}
	if eligible, _ := auth.LoginEligible(login, withoutConfiguredClient); eligible {
		t.Fatal("configured login accepted after the configured client was removed")
	}
}

func TestLoadLoginRejectsMissingProvenanceAndNullToken(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{name: "null token", raw: `{"resource":"https://srv.example.com","token_url":"https://issuer.example.com/token","client":{"source":"configured","id":"client"},"token":null}`},
		{name: "missing source", raw: `{"resource":"https://srv.example.com","token_url":"https://issuer.example.com/token","client":{"id":"client"},"token":{"access_token":"secret"}}`},
		{name: "unknown source", raw: `{"resource":"https://srv.example.com","token_url":"https://issuer.example.com/token","client":{"source":"future","id":"client"},"token":{"access_token":"secret"}}`},
		{name: "configured secret", raw: `{"resource":"https://srv.example.com","token_url":"https://issuer.example.com/token","client":{"source":"configured","id":"client","secret":"must-not-be-saved"},"token":{"access_token":"secret"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			testutil.WriteFile(t, filepath.Join(dir, "internal", "srv.token.json"), tc.raw)
			if _, err := auth.LoadLogin(dir, "srv"); err == nil {
				t.Fatal("LoadLogin accepted a record without valid provenance")
			}
		})
	}
}

func TestLoginStateNeedsLogin(t *testing.T) {
	for state, want := range map[auth.LoginState]bool{
		auth.Missing: true, auth.Unreadable: true, auth.ForOtherSettings: true,
		auth.Expired: true, auth.Refreshable: false, auth.Valid: false,
	} {
		if got := state.NeedsLogin(); got != want {
			t.Errorf("%v.NeedsLogin() = %v, want %v", state, got, want)
		}
	}
}

func loginServerConfig() config.ServerConfig {
	return config.ServerConfig{
		Name: "srv",
		URL:  "https://srv.example.com",
		Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, ClientID: "client"},
	}
}
