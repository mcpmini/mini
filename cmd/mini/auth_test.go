package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
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

	opener := authOpener(
		&config.Config{BrowserCommand: "global-cmd", DisableAuthBrowserOpen: true},
		oauthServerWithBrowserCmd("echo"),
	)
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

func oauthServerFor(tokenServer *authtest.TokenServer) *config.ServerConfig {
	return &config.ServerConfig{
		Name: "svc", Transport: "http", URL: tokenServer.Srv.URL + "/mcp", Auth: tokenServer.AuthConfig(),
	}
}

func TestLogIn_savesTokenAndReportsSuccess(t *testing.T) {
	tokenServer := authtest.NewTokenServer(t)
	browserThat(t, func(authURL string) { authtest.CompleteAuthorization(t, authURL, "test-auth-code") })
	dir := t.TempDir()
	var out bytes.Buffer

	err := logIn(logInParams{configDir: dir, cfg: &config.Config{}, sc: oauthServerFor(tokenServer), out: &out})
	if err != nil {
		t.Fatalf("logIn: %v", err)
	}
	saved, err := auth.LoadLogin(dir, "svc")
	if err != nil || saved.Token.AccessToken != tokenServer.AccessToken {
		t.Errorf("saved token = %+v (err %v), want the token server's %q", saved, err, tokenServer.AccessToken)
	}
	if !strings.HasPrefix(out.String(), "authorized svc (") {
		t.Errorf("output = %q, want the authorized line for svc", out.String())
	}
}

func unreachableDaemon() (string, string, error) { return "", "", errors.New("no daemon in tests") }

func fakeLoginDaemon(t *testing.T, handler http.HandlerFunc) daemonLocator {
	t.Helper()
	socket := filepath.Join(testutil.ShortTempDir(t), "d.sock")
	testutil.StartUnixServer(t, socket, handler)
	return func() (string, string, error) { return socket, "test-token", nil }
}

func streamLoginLines(lines ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, line := range lines {
			fmt.Fprintln(w, line) //nolint:errcheck // a failed write shows as the test's missing line
		}
	}
}

const daemonLoginURL = `{"url":"https://as.example.com/authorize?state=s"}`

func TestLogIn_unreachableDaemonLogsInHere(t *testing.T) {
	tokenServer := authtest.NewTokenServer(t)
	browserThat(t, func(authURL string) { authtest.CompleteAuthorization(t, authURL, "test-auth-code") })
	dir := t.TempDir()

	err := logIn(logInParams{
		configDir: dir, cfg: &config.Config{}, sc: oauthServerFor(tokenServer), out: io.Discard,
		locateDaemon: unreachableDaemon,
	})

	if _, loadErr := auth.LoadLogin(dir, "svc"); err != nil || loadErr != nil {
		t.Errorf("logIn = %v, saved login error = %v; want the login run and saved here", err, loadErr)
	}
}

func TestLogIn_daemonLoginFailureIsReturnedWithoutLoggingInHere(t *testing.T) {
	refuse := func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "login_failed", "message": "resolve oauth endpoints: boom",
		})
	}
	failed := `{"error":"login_failed","message":"auth flow: access_denied"}`
	for _, tc := range []struct {
		name   string
		daemon http.HandlerFunc
		want   string
	}{
		{"refused before the URL", refuse, "resolve oauth endpoints: boom"},
		{"failed outcome", streamLoginLines(daemonLoginURL, failed), "auth flow: access_denied"},
		{"stream ending without an outcome", streamLoginLines(daemonLoginURL), "login interrupted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokenServer := authtest.NewTokenServer(t)
			browserThat(t, func(string) {})
			var out bytes.Buffer

			err := logIn(logInParams{
				configDir: t.TempDir(), cfg: &config.Config{}, sc: oauthServerFor(tokenServer), out: &out,
				locateDaemon: fakeLoginDaemon(t, tc.daemon),
			})

			if err == nil || err.Error() != tc.want {
				t.Errorf("logIn = %v, want %q", err, tc.want)
			}
			if out.Len() > 0 || tokenServer.Hits.Load() != 0 {
				t.Errorf(
					"output %q, token server hit %d times; want no output and no login here",
					out.String(),
					tokenServer.Hits.Load(),
				)
			}
		})
	}
}

func TestLogIn_daemonConnectErrorIsPrintedAfterAuthorized(t *testing.T) {
	browserThat(t, func(string) {})
	authorized := `{"ok":true,"expires":"2030-01-02T03:04:05Z","connect_error":"initialize: boom"}`
	var out bytes.Buffer

	err := logIn(logInParams{
		configDir: t.TempDir(), cfg: &config.Config{}, sc: oauthServerFor(authtest.NewTokenServer(t)), out: &out,
		locateDaemon: fakeLoginDaemon(t, streamLoginLines(daemonLoginURL, authorized)),
	})

	want := "authorized svc (expires 2030-01-02T03:04:05Z)\nconnecting to svc failed: initialize: boom; run `mini auth svc` to try again\n"
	if err != nil || out.String() != want {
		t.Errorf("logIn = %v, output %q; want %q", err, out.String(), want)
	}
}
