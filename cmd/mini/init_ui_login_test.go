package main

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/mcpmini/mini/cmd/mini/initcmd/tui"
	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/auth/authtest"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func oauthServerIn(t *testing.T, tokenServer *authtest.TokenServer) string {
	t.Helper()
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{
		Name: "svc", Transport: "http", URL: tokenServer.Srv.URL + "/mcp", Auth: tokenServer.AuthConfig(),
	})
	return dir
}

func browserThat(t *testing.T, open func(authURL string)) {
	t.Helper()
	orig := openBrowser
	openBrowser = func(authURL string) error {
		open(authURL)
		return nil
	}
	t.Cleanup(func() { openBrowser = orig })
}

func TestInitLoginStarter_aCompletedLoginSavesTheToken(t *testing.T) {
	tokenServer := authtest.NewTokenServer(t)
	dir := oauthServerIn(t, tokenServer)
	browserThat(t, func(authURL string) { go authtest.CompleteAuthorization(t, authURL, "test-auth-code") })

	login, err := initLoginStarter(dir)(t.Context(), dir, "svc")
	if err != nil || login.URL == "" {
		t.Fatalf("start = %+v, %v; want a login with its URL", login, err)
	}
	if err := login.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if _, err := auth.LoadLogin(dir, "svc"); err != nil {
		t.Errorf("token after the login: %v, want it saved", err)
	}
}

func TestInitLoginStarter_aCancelledLoginSavesAndLogsNothing(t *testing.T) {
	tokenServer := authtest.NewTokenServer(t)
	dir := oauthServerIn(t, tokenServer)
	browserThat(t, func(string) {})
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })
	ctx, cancel := context.WithCancel(t.Context())

	login, err := initLoginStarter(dir)(ctx, dir, "svc")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := login.Wait(); err == nil {
		t.Error("Wait after cancel returned nil, want the cancellation")
	}
	if _, err := auth.LoadLogin(dir, "svc"); !auth.IsNotFound(err) {
		t.Errorf("token after a cancelled login: %v, want none", err)
	}
	if logged.Len() > 0 {
		t.Errorf("logged %q; a login the user cancelled isn't a failure worth a log line", logged.String())
	}
}

func TestInitLoginStarter_aServerWithoutOAuthIsAnError(t *testing.T) {
	dir := t.TempDir()
	configtest.WriteServer(t, dir, config.ServerConfig{Name: "local", Command: "run"})
	if _, err := initLoginStarter(dir)(t.Context(), dir, "local"); err == nil {
		t.Error("start on a stdio server returned no error")
	}
}

func useDaemon(t *testing.T, reach daemonLocator) {
	t.Helper()
	orig := daemonLocatorFor
	daemonLocatorFor = func(string) daemonLocator { return reach }
	t.Cleanup(func() { daemonLocatorFor = orig })
}

func TestInitLoginStarter_aServerThisRunAddsNeverContactsTheDaemon(t *testing.T) {
	stage := oauthServerIn(t, authtest.NewTokenServer(t))
	browserThat(t, func(authURL string) { go authtest.CompleteAuthorization(t, authURL, "test-auth-code") })
	contacted := false
	useDaemon(t, func() (string, string, error) { contacted = true; return unreachableDaemon() })

	login, err := initLoginStarter(t.TempDir())(t.Context(), stage, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if err := login.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if contacted {
		t.Error("a login saving to the stage contacted the daemon")
	}
	if _, err := auth.LoadLogin(stage, "svc"); err != nil {
		t.Errorf("token in the stage after the login: %v, want it saved there", err)
	}
}

func TestInitLoginStarter_aServerInMiniLogsInThroughTheDaemonPrintingNothing(t *testing.T) {
	dir := oauthServerIn(t, authtest.NewTokenServer(t))
	browserThat(t, func(string) {})
	authorized := `{"ok":true,"expires":null,"connect_error":"initialize: boom"}`
	useDaemon(t, fakeLoginDaemon(t, streamLoginLines(daemonLoginURL, authorized)))
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })
	var login tui.Login
	var startErr, waitErr error
	var stdout string

	stderr := testutil.CaptureStderr(t, func() {
		stdout = testutil.CaptureStdout(t, func() {
			login, startErr = initLoginStarter(dir)(t.Context(), dir, "svc")
			if startErr == nil {
				waitErr = login.Wait()
			}
		})
	})

	if startErr != nil || waitErr != nil || login.URL != "https://as.example.com/authorize?state=s" {
		t.Fatalf("login = %+v, start %v, wait %v; want the daemon's login to succeed", login, startErr, waitErr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("stdout %q, stderr %q; want nothing, init's screen owns the terminal", stdout, stderr)
	}
	if !strings.Contains(logged.String(), "authorized svc, but connecting to it failed: initialize: boom") {
		t.Errorf("logged %q, want the connect error", logged.String())
	}
	if _, err := auth.LoadLogin(dir, "svc"); !auth.IsNotFound(err) {
		t.Errorf("token saved here: %v, want the daemon to own the save", err)
	}
}
