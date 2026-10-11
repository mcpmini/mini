//go:build test

package initcmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

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

		result := s.commit([]string{"linear"})
		committed, failed := result.committed, result.failed

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

		if failed := s.commit([]string{"linear"}).failed; len(failed) > 0 {
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

		result := s.commit([]string{"linear"})
		committed, failed := result.committed, result.failed

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

	t.Run("a stage a killed run left a day ago is removed, a recent one kept", func(t *testing.T) {
		configDir := t.TempDir()
		old := filepath.Join(configDir, "internal", "init-stage-old")
		recent := filepath.Join(configDir, "internal", "init-stage-recent")
		testutil.WriteFile(t, filepath.Join(old, "internal", "linear.token.json"), `{}`)
		testutil.WriteFile(t, filepath.Join(recent, "internal", "notes.token.json"), `{}`)
		dayAgo := time.Now().Add(-staleStageAge - time.Minute)
		if err := os.Chtimes(old, dayAgo, dayAgo); err != nil {
			t.Fatal(err)
		}

		newTestStage(t, configDir)

		if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("old stage: %v, want it removed with the login it held", err)
		}
		if _, err := os.Stat(recent); err != nil {
			t.Errorf("recent stage: %v, want it kept: another init may be using it", err)
		}
	})

	t.Run("a stage its run still saves to isn't swept, however old it was", func(t *testing.T) {
		configDir := t.TempDir()
		inUse := newTestStage(t, configDir)
		dayAgo := time.Now().Add(-staleStageAge - time.Minute)
		if err := os.Chtimes(inUse.dir, dayAgo, dayAgo); err != nil {
			t.Fatal(err)
		}

		inUse.touch()
		newTestStage(t, configDir)

		if _, err := os.Stat(inUse.dir); err != nil {
			t.Errorf("stage in use: %v, want it kept", err)
		}
	})

	t.Run("a server name taken in another case keeps mini's login", func(t *testing.T) {
		configDir := t.TempDir()
		s := newTestStage(t, configDir)
		stageServer(t, s, "linear")
		testutil.WriteFile(t, filepath.Join(configDir, "servers", "Linear.yaml"), "url: https://theirs.example/mcp\n")
		theirs := filepath.Join(configDir, "internal", "Linear.token.json")
		testutil.WriteFile(t, theirs, `{"access_token":"theirs"}`)

		failed := s.commit([]string{"linear"}).failed

		if len(failed) != 1 || !errors.Is(failed[0].Err, errAddedOutsideInit) {
			t.Errorf("failed = %v, want linear reported as added outside init", failed)
		}
		if got := readOrMissing(t, theirs); got != `{"access_token":"theirs"}` {
			t.Errorf("Linear's token = %q, want it untouched", got)
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
