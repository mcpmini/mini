package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/cmd/mini/importers"
	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

func TestRunLoginStepAllContinuesAfterFailure(t *testing.T) {
	dir := loginStepConfig(t, "first", "second")
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	var authorized []string
	runLoginStep(loginStepParams{
		configDir: dir,
		ask:       func(string) string { return "a" },
		logIn:     recordAuthorization(&authorized, map[string]error{"first": errors.New("denied")}),
		out:       out,
		errOut:    errOut,
	})
	if !reflect.DeepEqual(authorized, []string{"first", "second"}) {
		t.Errorf("authorized = %v, want [first second]", authorized)
	}
	if !strings.Contains(errOut.String(), "login failed for first: denied") {
		t.Errorf("error output = %q", errOut.String())
	}
	assertReminded(t, out.String(), []string{"first", "second"}, []string{"first"})
}

func TestRunLoginStepAutoYesSkipsPrompts(t *testing.T) {
	dir := loginStepConfig(t, "first")
	called := false
	out := &bytes.Buffer{}
	runLoginStep(loginStepParams{
		configDir: dir,
		autoYes:   true,
		ask:       func(string) string { called = true; return "a" },
		confirm:   func(string) bool { called = true; return true },
		out:       out,
		errOut:    &bytes.Buffer{},
	})
	if called || !strings.Contains(out.String(), "  mini auth first\n") {
		t.Errorf("auto yes prompts=%v output=%q", called, out.String())
	}
}

func TestRunLoginStepSkipsBundledOAuthForImportedStdioServer(t *testing.T) {
	dir := t.TempDir()
	if err := importers.AddServerYAML(dir, "slack", importers.ServerYAML{
		Command: "npx",
		Args:    []string{"server-slack"},
	}); err != nil {
		t.Fatal(err)
	}
	called := false
	out := &bytes.Buffer{}
	runLoginStep(loginStepParams{
		configDir: dir,
		ask:       func(string) string { called = true; return "a" },
		logIn:     func(logInParams) (*oauth2.Token, error) { called = true; return nil, nil },
		out:       out,
		errOut:    &bytes.Buffer{},
	})
	if called || out.Len() != 0 {
		t.Errorf("bundled OAuth authorization queued: called=%v output=%q", called, out.String())
	}
}

func TestRunLoginStepWarnsAndSkipsWhenConfigFailsToLoad(t *testing.T) {
	const unsetVar = "MINI_TEST_UNSET_AUTH_PASS_VAR"
	t.Setenv(unsetVar, "")
	os.Unsetenv(unsetVar) //nolint:errcheck
	dir := t.TempDir()
	writeLoginStepFile(t, filepath.Join(dir, "servers", "broken.yaml"), "command: npx\nenv: [\"K=${"+unsetVar+"}\"]\n")
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	runLoginStep(loginStepParams{configDir: dir, out: out, errOut: errOut})
	if !strings.Contains(errOut.String(), "skipping OAuth login") {
		t.Errorf("stderr = %q, want skip warning", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestRunLoginStepListingShowsReasonNextToServerName(t *testing.T) {
	dir := loginStepConfig(t, "fresh", "expired", "corrupt")
	expired := &oauth2.Token{AccessToken: "t", Expiry: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := auth.Save(dir, "expired", expired); err != nil {
		t.Fatal(err)
	}
	writeLoginStepFile(t, filepath.Join(dir, "internal", "corrupt.token.json"), "not json")
	out := &bytes.Buffer{}
	runLoginStep(loginStepParams{configDir: dir, ask: func(string) string { return "s" }, out: out, errOut: &bytes.Buffer{}})
	for _, want := range []string{"fresh (no token)", "expired (token expired)", "corrupt (token unreadable: "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("listing missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunLoginStepAnswers(t *testing.T) {
	for _, tc := range []struct {
		name           string
		answer         string
		wantAuthorized []string
		wantReminded   []string
	}{
		{name: "skip", answer: "s", wantReminded: []string{"first", "second"}},
		{name: "all", answer: "all", wantAuthorized: []string{"first", "second"}},
		{name: "ALL", answer: "ALL", wantAuthorized: []string{"first", "second"}},
		{name: "p", answer: "p", wantAuthorized: []string{"second"}, wantReminded: []string{"first"}},
		{name: "pick", answer: "pick", wantAuthorized: []string{"second"}, wantReminded: []string{"first"}},
		{name: "unrecognized", answer: "maybe", wantReminded: []string{"first", "second"}},
		{name: "empty", answer: "", wantReminded: []string{"first", "second"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := loginStepConfig(t, "first", "second")
			out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
			var authorized []string
			runLoginStep(loginStepParams{
				configDir: dir,
				ask:       func(string) string { return tc.answer },
				confirm:   confirmAnswers(false, true),
				logIn:     recordAuthorization(&authorized, nil),
				out:       out,
				errOut:    errOut,
			})
			if !reflect.DeepEqual(authorized, tc.wantAuthorized) {
				t.Errorf("authorized = %v, want %v", authorized, tc.wantAuthorized)
			}
			assertReminded(t, out.String(), []string{"first", "second"}, tc.wantReminded)
			if errOut.Len() != 0 {
				t.Errorf("unexpected errors: %s", errOut.String())
			}
		})
	}
}

func TestRunLoginStepOmitsServersWithUsableTokens(t *testing.T) {
	dir := loginStepConfig(t, "ok", "refresh", "need")
	saveTestToken(t, dir, "ok", &oauth2.Token{AccessToken: "t", Expiry: time.Now().Add(time.Hour)})
	saveTestToken(t, dir, "refresh", &oauth2.Token{AccessToken: "t", RefreshToken: "r", Expiry: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)})
	out := &bytes.Buffer{}
	var authorized []string
	runLoginStep(loginStepParams{
		configDir: dir,
		ask:       func(string) string { return "a" },
		logIn:     recordAuthorization(&authorized, nil),
		out:       out,
		errOut:    &bytes.Buffer{},
	})
	if !reflect.DeepEqual(authorized, []string{"need"}) {
		t.Errorf("authorized = %v, want [need]", authorized)
	}
	if !strings.Contains(out.String(), "  need (no token)\n") || strings.Contains(out.String(), "  ok ") || strings.Contains(out.String(), "  refresh ") {
		t.Errorf("listing should name only need:\n%s", out.String())
	}
}

func TestRunLoginStepOmitsDisabledServers(t *testing.T) {
	dir := loginStepConfig(t, "on")
	disabled := false
	off := config.ServerConfig{Name: "off", Transport: "http", Enabled: &disabled, Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2}}
	if _, err := ops.AddServer(dir, off); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	var authorized []string
	runLoginStep(loginStepParams{
		configDir: dir,
		ask:       func(string) string { return "a" },
		logIn:     recordAuthorization(&authorized, nil),
		out:       out,
		errOut:    &bytes.Buffer{},
	})
	if !reflect.DeepEqual(authorized, []string{"on"}) {
		t.Errorf("authorized = %v, want [on]", authorized)
	}
	if strings.Contains(out.String(), "off") {
		t.Errorf("disabled server listed or reminded:\n%s", out.String())
	}
}

func saveTestToken(t *testing.T, dir, name string, token *oauth2.Token) {
	t.Helper()
	if err := auth.Save(dir, name, token); err != nil {
		t.Fatal(err)
	}
}

func TestRunLoginStepPassesLoadedConfigAndServerToLogIn(t *testing.T) {
	dir := loginStepConfig(t, "first")
	writeLoginStepFile(t, filepath.Join(dir, "config.yaml"), "disable_auth_browser_open: true\n")
	out := &bytes.Buffer{}
	var got logInParams
	runLoginStep(loginStepParams{
		configDir: dir,
		ask:       func(string) string { return "a" },
		logIn:     func(p logInParams) (*oauth2.Token, error) { got = p; return &oauth2.Token{}, nil },
		out:       out,
		errOut:    &bytes.Buffer{},
	})
	if got.sc == nil || got.sc.Name != "first" || got.sc.Auth == nil || got.sc.Auth.Type != config.AuthTypeOAuth2 {
		t.Fatalf("sc = %+v, want first with oauth2 auth", got.sc)
	}
	if got.configDir != dir || got.cfg == nil || !got.cfg.DisableAuthBrowserOpen || got.out != out {
		t.Errorf("logIn params = %+v, want config dir, loaded config, and step stdout", got)
	}
}

func assertReminded(t *testing.T, output string, all, want []string) {
	t.Helper()
	for _, name := range all {
		reminded := strings.Contains(output, "  mini auth "+name+"\n")
		if reminded != slices.Contains(want, name) {
			t.Errorf("reminder for %s = %v, want %v\n%s", name, reminded, !reminded, output)
		}
	}
}

func writeLoginStepFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func loginStepConfig(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		sc := config.ServerConfig{Name: name, Transport: "http", Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2}}
		if _, err := ops.AddServer(dir, sc); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func confirmAnswers(answers ...bool) func(string) bool {
	return func(string) bool {
		answer := answers[0]
		answers = answers[1:]
		return answer
	}
}

func recordAuthorization(calls *[]string, failures map[string]error) func(logInParams) (*oauth2.Token, error) {
	return func(p logInParams) (*oauth2.Token, error) {
		*calls = append(*calls, p.sc.Name)
		if err := failures[p.sc.Name]; err != nil {
			return nil, err
		}
		return &oauth2.Token{AccessToken: "token"}, nil
	}
}
