package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
)

func oauthServerWithBrowserCmd(cmd string) config.ServerConfig {
	return config.ServerConfig{Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2, BrowserCmd: cmd}}
}

func TestAuthOpener_usesPlatformDefaultWhenNeitherSet(t *testing.T) {
	var called bool
	orig := openBrowser
	openBrowser = func(url string) error { called = true; return nil }
	t.Cleanup(func() { openBrowser = orig })

	opener := authOpener(&config.Config{}, config.ServerConfig{})
	_ = opener("http://example.com")
	if !called {
		t.Error("expected platform opener to be called when neither per-server nor global cmd is set")
	}
}

func TestAuthOpener_skipsPlatformDefaultWhenCmdSet(t *testing.T) {
	var called bool
	orig := openBrowser
	openBrowser = func(url string) error { called = true; return nil }
	t.Cleanup(func() { openBrowser = orig })

	opener := authOpener(&config.Config{}, oauthServerWithBrowserCmd("echo"))
	_ = opener("http://example.com")
	if called {
		t.Error("platform opener should not be called when per-server cmd is set")
	}
}

func TestAuthOpener_disabledSkipsAll(t *testing.T) {
	var called bool
	orig := openBrowser
	openBrowser = func(url string) error { called = true; return nil }
	t.Cleanup(func() { openBrowser = orig })

	opener := authOpener(&config.Config{BrowserCommand: "global-cmd", DisableAuthBrowserOpen: true}, oauthServerWithBrowserCmd("echo"))
	if err := opener("http://example.com"); err != nil {
		t.Errorf("disabled opener returned error: %v", err)
	}
	if called {
		t.Error("platform opener should not be called when disabled")
	}
}

func TestPrintAuthResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expiry time.Time
		want   string
	}{
		{"zero expiry", time.Time{}, "authorized x (no expiry)\n"},
		{"fixed expiry", time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC), "authorized x (expires 2030-01-02T03:04:05Z)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			printAuthResult(out, "x", tc.expiry)
			if out.String() != tc.want {
				t.Errorf("output = %q, want %q", out.String(), tc.want)
			}
		})
	}
}

func TestLogIn_savesTokenAndReportsSuccess(t *testing.T) {
	tokenServer := authtest.NewTokenServer(t)
	orig := openBrowser
	openBrowser = func(authURL string) error {
		authtest.CompleteAuthorization(t, authURL, "test-auth-code")
		return nil
	}
	t.Cleanup(func() { openBrowser = orig })
	dir := t.TempDir()
	sc := &config.ServerConfig{Name: "svc", Transport: "http", URL: tokenServer.Srv.URL + "/mcp", Auth: tokenServer.AuthConfig()}
	var out bytes.Buffer

	token, err := logIn(logInParams{configDir: dir, cfg: &config.Config{}, sc: sc, out: &out})

	if err != nil {
		t.Fatalf("logIn: %v", err)
	}
	saved, err := auth.Load(dir, "svc")
	if err != nil || saved.AccessToken != token.AccessToken {
		t.Errorf("saved token = %+v (err %v), want the returned token %q", saved, err, token.AccessToken)
	}
	if !strings.HasPrefix(out.String(), "authorized svc (") {
		t.Errorf("output = %q, want the authorized line for svc", out.String())
	}
}
