//go:build test

package initcmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func newTestStage(t *testing.T, configDir string) *stage {
	t.Helper()
	s, err := newStage(configDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.discard() }) //nolint:errcheck // a leftover temp dir doesn't change the test's outcome
	return s
}

func readOrMissing(t *testing.T, path string) string {
	t.Helper()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return "<missing>"
	}
	return string(testutil.ReadFile(t, path))
}

func TestStage(t *testing.T) {
	t.Run("the stage holds mini's settings, servers and logins", func(t *testing.T) {
		configDir := t.TempDir()
		testutil.WriteFile(t, filepath.Join(configDir, "config.yaml"), "tool_mode: compact\n")
		testutil.WriteFile(t, filepath.Join(configDir, "servers", "notes.yaml"), "url: https://notes.example/mcp\n")
		testutil.WriteFile(t, filepath.Join(configDir, "internal", "notes.token.json"), `{"access_token":"a"}`)

		s := newTestStage(t, configDir)

		for _, rel := range []string{"config.yaml", "servers/notes.yaml", "internal/notes.token.json"} {
			if got, want := readOrMissing(
				t,
				filepath.Join(s.dir, rel),
			), readOrMissing(
				t,
				filepath.Join(configDir, rel),
			); got != want {
				t.Errorf("staged %s = %q, want mini's %q", rel, got, want)
			}
		}
	})

	t.Run("commit brings the staged servers and logins into mini", func(t *testing.T) {
		configDir := t.TempDir()
		s := newTestStage(t, configDir)
		testutil.WriteFile(t, filepath.Join(s.dir, "servers", "linear.yaml"), "url: https://linear.example/mcp\n")
		testutil.WriteFile(t, filepath.Join(s.dir, "internal", "linear.token.json"), `{"access_token":"l"}`)

		if failed := s.commit(); len(failed) > 0 {
			t.Fatalf("commit failed: %v", failed)
		}

		if got := readOrMissing(
			t,
			filepath.Join(configDir, "servers", "linear.yaml"),
		); got != "url: https://linear.example/mcp\n" {
			t.Errorf("linear.yaml = %q, want the staged server", got)
		}
		if got := readOrMissing(
			t,
			filepath.Join(configDir, "internal", "linear.token.json"),
		); got != `{"access_token":"l"}` {
			t.Errorf("linear token = %q, want the staged login", got)
		}
	})

	t.Run("a file the run removed is removed from mini", func(t *testing.T) {
		configDir := t.TempDir()
		token := filepath.Join(configDir, "internal", "old.token.json")
		testutil.WriteFile(t, token, `{"access_token":"stale"}`)
		s := newTestStage(t, configDir)
		if err := os.Remove(filepath.Join(s.dir, "internal", "old.token.json")); err != nil {
			t.Fatal(err)
		}

		if failed := s.commit(); len(failed) > 0 {
			t.Fatalf("commit failed: %v", failed)
		}

		if got := readOrMissing(t, token); got != "<missing>" {
			t.Errorf("old token = %q, want it removed as in the stage", got)
		}
	})

	t.Run("a server added to mini during the run keeps mini's version and is reported", func(t *testing.T) {
		configDir := t.TempDir()
		s := newTestStage(t, configDir)
		testutil.WriteFile(t, filepath.Join(s.dir, "servers", "linear.yaml"), "url: https://staged.example/mcp\n")
		testutil.WriteFile(t, filepath.Join(configDir, "servers", "linear.yaml"), "url: https://theirs.example/mcp\n")

		failed := s.commit()

		if len(failed) != 1 || failed[0].Name != "linear" || !errors.Is(failed[0].Err, errChangedOutsideInit) {
			t.Errorf("failed = %v, want linear reported as changed outside init", failed)
		}
		if got := readOrMissing(
			t,
			filepath.Join(configDir, "servers", "linear.yaml"),
		); got != "url: https://theirs.example/mcp\n" {
			t.Errorf("linear.yaml = %q, want the version added outside init kept", got)
		}
	})

	t.Run("a token refreshed in mini during the run keeps the newer token silently", func(t *testing.T) {
		configDir := t.TempDir()
		token := filepath.Join(configDir, "internal", "notes.token.json")
		testutil.WriteFile(t, token, `{"access_token":"old"}`)
		s := newTestStage(t, configDir)
		testutil.WriteFile(t, filepath.Join(s.dir, "internal", "notes.token.json"), `{"access_token":"staged"}`)
		testutil.WriteFile(t, token, `{"access_token":"refreshed"}`)

		if failed := s.commit(); len(failed) > 0 {
			t.Errorf("failed = %v, want none: a newer token isn't a problem to report", failed)
		}
		if got := readOrMissing(t, token); got != `{"access_token":"refreshed"}` {
			t.Errorf("token = %q, want the refreshed one kept", got)
		}
	})

	t.Run("an unchanged file is left alone even if mini changed it", func(t *testing.T) {
		configDir := t.TempDir()
		server := filepath.Join(configDir, "servers", "notes.yaml")
		testutil.WriteFile(t, server, "url: https://old.example/mcp\n")
		s := newTestStage(t, configDir)
		testutil.WriteFile(t, server, "url: https://edited.example/mcp\n")

		if failed := s.commit(); len(failed) > 0 {
			t.Errorf("failed = %v, want none: the run didn't touch notes", failed)
		}
		if got := readOrMissing(t, server); got != "url: https://edited.example/mcp\n" {
			t.Errorf("notes.yaml = %q, want the edit made during the run kept", got)
		}
	})

	t.Run("discard removes the stage and leaves mini as it was", func(t *testing.T) {
		configDir := filepath.Join(t.TempDir(), "config")
		s := newTestStage(t, configDir)
		testutil.WriteFile(t, filepath.Join(s.dir, "servers", "linear.yaml"), "url: https://linear.example/mcp\n")

		if err := s.discard(); err != nil {
			t.Fatal(err)
		}

		for _, dir := range []string{s.dir, configDir} {
			if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s: %v, want it missing", dir, err)
			}
		}
	})
}
