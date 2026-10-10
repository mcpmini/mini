//go:build test

package initcmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func newTestStage(t *testing.T, configDir string) *stage {
	t.Helper()
	s := newStage(configDir)
	if err := s.create(); err != nil {
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

func stageServer(t *testing.T, s *stage, name string) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(s.dir, "servers", name+".yaml"), "url: https://"+name+".example/mcp\n")
	testutil.WriteFile(t, filepath.Join(s.dir, "internal", name+".token.json"), `{"access_token":"`+name+`"}`)
}

func TestStage(t *testing.T) {
	t.Run("the stage reads mini's settings but holds none of its servers", func(t *testing.T) {
		configDir := t.TempDir()
		testutil.WriteFile(t, filepath.Join(configDir, "config.yaml"), "tool_mode: compact\n")
		testutil.WriteFile(t, filepath.Join(configDir, "servers", "notes.yaml"), "url: https://notes.example/mcp\n")

		s := newTestStage(t, configDir)

		if got := readOrMissing(t, filepath.Join(s.dir, "config.yaml")); got != "tool_mode: compact\n" {
			t.Errorf("staged config.yaml = %q, want mini's settings", got)
		}
		if got := readOrMissing(t, filepath.Join(s.dir, "servers", "notes.yaml")); got != "<missing>" {
			t.Errorf("staged notes.yaml = %q, want mini's servers left where they are", got)
		}
	})

	t.Run("commit brings a staged server and its login into mini", func(t *testing.T) {
		configDir := t.TempDir()
		s := newTestStage(t, configDir)
		stageServer(t, s, "linear")

		committed, failed := s.commit([]string{"linear"})

		if !slices.Equal(committed, []string{"linear"}) || len(failed) > 0 {
			t.Fatalf("commit = %v, %v; want linear committed", committed, failed)
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
		); got != `{"access_token":"linear"}` {
			t.Errorf("linear token = %q, want the staged login", got)
		}
	})

	t.Run("a token mini still holds from an earlier server of the same name is removed", func(t *testing.T) {
		configDir := t.TempDir()
		stale := filepath.Join(configDir, "internal", "linear.dcr.json")
		testutil.WriteFile(t, stale, `{"client_id":"old"}`)
		s := newTestStage(t, configDir)
		stageServer(t, s, "linear")

		if _, failed := s.commit([]string{"linear"}); len(failed) > 0 {
			t.Fatalf("commit failed: %v", failed)
		}

		if got := readOrMissing(t, stale); got != "<missing>" {
			t.Errorf("old client = %q, want it removed: it was registered for another server", got)
		}
	})

	t.Run("a server added to mini during the run keeps mini's version and login", func(t *testing.T) {
		configDir := t.TempDir()
		s := newTestStage(t, configDir)
		stageServer(t, s, "linear")
		testutil.WriteFile(t, filepath.Join(configDir, "servers", "linear.yaml"), "url: https://theirs.example/mcp\n")
		testutil.WriteFile(t, filepath.Join(configDir, "internal", "linear.token.json"), `{"access_token":"theirs"}`)

		committed, failed := s.commit([]string{"linear"})

		if len(committed) > 0 || len(failed) != 1 || !errors.Is(failed[0].Err, errAddedOutsideInit) {
			t.Errorf("commit = %v, %v; want linear reported as added outside init", committed, failed)
		}
		if got := readOrMissing(
			t,
			filepath.Join(configDir, "servers", "linear.yaml"),
		); got != "url: https://theirs.example/mcp\n" {
			t.Errorf("linear.yaml = %q, want the version added outside init kept", got)
		}
		if got := readOrMissing(
			t,
			filepath.Join(configDir, "internal", "linear.token.json"),
		); got != `{"access_token":"theirs"}` {
			t.Errorf("linear token = %q, want the login of the version kept", got)
		}
	})

	t.Run("discard removes the stage and leaves mini's servers as they were", func(t *testing.T) {
		configDir := t.TempDir()
		s := newTestStage(t, configDir)
		stageServer(t, s, "linear")

		if err := s.discard(); err != nil {
			t.Fatal(err)
		}

		if _, err := os.Stat(s.dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("stage: %v, want it removed", err)
		}
		if got := readOrMissing(t, filepath.Join(configDir, "servers", "linear.yaml")); got != "<missing>" {
			t.Errorf("linear.yaml = %q, want nothing in mini", got)
		}
	})
}
