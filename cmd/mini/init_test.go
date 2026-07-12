package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

func TestIsSelfEntry(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("current executable is self", func(t *testing.T) {
		if !isSelfEntry(self, self) {
			t.Error("expected self to match self")
		}
	})
	t.Run("symlink to self is self", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "mini-link")
		if err := os.Symlink(self, link); err != nil {
			t.Skip("cannot create symlink:", err)
		}
		if !isSelfEntry(link, self) {
			t.Error("expected symlink to self to be detected as self")
		}
	})
	t.Run("unrelated binary is not self", func(t *testing.T) {
		if isSelfEntry("/usr/bin/env", self) {
			t.Error("expected /usr/bin/env to not be self")
		}
	})
	t.Run("empty cmd is not self", func(t *testing.T) {
		if isSelfEntry("", self) {
			t.Error("expected empty cmd to return false")
		}
	})
}

func TestRunAuthPassSkipPrintsReminders(t *testing.T) {
	dir := authPassConfig(t, "first", "second")
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	err := runAuthPass(authPassParams{configDir: dir, choose: func(string) string { return "s" }, out: out, err: errOut})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "mini auth first") || !strings.Contains(got, "mini auth second") {
		t.Errorf("output missing reminders:\n%s", got)
	}
	if errOut.Len() != 0 {
		t.Errorf("unexpected errors: %s", errOut.String())
	}
}

func TestRunAuthPassPickHonorsAnswers(t *testing.T) {
	dir := authPassConfig(t, "first", "second")
	var authorized []string
	err := runAuthPass(authPassParams{
		configDir: dir,
		choose:    func(string) string { return "p" },
		confirm:   confirmAnswers(true, false),
		authorize: recordAuthorization(&authorized, nil),
		out:       &bytes.Buffer{},
		err:       &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(authorized, []string{"first"}) {
		t.Errorf("authorized = %v, want [first]", authorized)
	}
}

func TestRunAuthPassAllContinuesAfterFailure(t *testing.T) {
	dir := authPassConfig(t, "first", "second")
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	var authorized []string
	err := runAuthPass(authPassParams{
		configDir: dir,
		choose:    func(string) string { return "a" },
		authorize: recordAuthorization(&authorized, map[string]error{"first": errors.New("denied")}),
		out:       out,
		err:       errOut,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(authorized, []string{"first", "second"}) {
		t.Errorf("authorized = %v, want [first second]", authorized)
	}
	if !strings.Contains(errOut.String(), "authorization failed for first: denied") {
		t.Errorf("error output = %q", errOut.String())
	}
	if !strings.Contains(out.String(), "mini auth first") || strings.Contains(out.String(), "mini auth second") {
		t.Errorf("reminders = %q", out.String())
	}
}

func TestRunAuthPassAutoYesSkipsPrompts(t *testing.T) {
	dir := authPassConfig(t, "first")
	called := false
	out := &bytes.Buffer{}
	err := runAuthPass(authPassParams{
		configDir: dir,
		autoYes:   true,
		choose:    func(string) string { called = true; return "a" },
		confirm:   func(string) bool { called = true; return true },
		out:       out,
		err:       &bytes.Buffer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if called || !strings.Contains(out.String(), "mini auth first") {
		t.Errorf("auto yes prompts=%v output=%q", called, out.String())
	}
}

func authPassConfig(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		sc := config.ServerConfig{Name: name, Auth: &config.AuthConfig{Type: config.AuthTypeOAuth2}}
		if err := ops.WriteServer(dir, sc); err != nil {
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

func recordAuthorization(calls *[]string, failures map[string]error) func(pkceFlowParams) (*oauth2.Token, error) {
	return func(p pkceFlowParams) (*oauth2.Token, error) {
		*calls = append(*calls, p.serverName)
		if err := failures[p.serverName]; err != nil {
			return nil, err
		}
		return &oauth2.Token{AccessToken: "token"}, nil
	}
}

func TestImportClaudeFormat_SkipsSelf(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configDir := t.TempDir()
	claudeJSON := `{
		"projects": {
			"/some/path": {
				"mcpServers": {
					"github": {"type": "http", "url": "https://api.githubcopilot.com/mcp"},
					"mini":   {"command": "` + self + `", "args": ["connect"]}
				}
			}
		}
	}`
	src := filepath.Join(t.TempDir(), "claude.json")
	if err := os.WriteFile(src, []byte(claudeJSON), 0600); err != nil {
		t.Fatal(err)
	}
	count := importClaudeFormat(configDir, src)
	if count != 1 {
		t.Errorf("imported %d servers, want 1 (mini should be skipped)", count)
	}
	if _, err := os.Stat(filepath.Join(configDir, "servers", "mini.yaml")); !os.IsNotExist(err) {
		t.Error("mini.yaml should not have been written")
	}
	if _, err := os.Stat(filepath.Join(configDir, "servers", "github.yaml")); err != nil {
		t.Error("github.yaml should have been written")
	}
}
