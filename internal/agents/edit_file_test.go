package agents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/testutil"
)

var editTime = time.Date(2026, 10, 4, 12, 30, 45, 0, time.UTC)

func appendLine(line string) func([]byte) ([]byte, error) {
	return func(data []byte) ([]byte, error) { return append(data, line+"\n"...), nil }
}

func TestEditFile_replacesTheFileAndBacksUpWhatItRead(t *testing.T) {
	path := filepath.Join(tempDir(t), "config.toml")
	testutil.WriteFile(t, path, "model = \"o3\"\n")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	backup, err := EditFile(path, appendLine("# edited"), editTime)

	if err != nil {
		t.Fatal(err)
	}
	if got := string(testutil.ReadFile(t, path)); got != "model = \"o3\"\n# edited\n" {
		t.Errorf("config = %q", got)
	}
	if want := filepath.Join(filepath.Dir(path), "config.minibackup.toml"); backup != want {
		t.Errorf("backup = %s, want %s", backup, want)
	}
	if got := string(testutil.ReadFile(t, backup)); got != "model = \"o3\"\n" {
		t.Errorf("backup = %q, want the original bytes", got)
	}
	requireMode(t, path, 0o640)
	requireMode(t, backup, 0o600)
}

func TestEditFile_editsASymlinksTargetAndKeepsTheLink(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(dir, "dotfiles", "settings.json")
	testutil.WriteFile(t, target, "{}\n")
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("cannot create symlink:", err)
	}

	backup, err := EditFile(link, appendLine("edited"), editTime)

	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link replaced by a regular file: %v", err)
	}
	if got := string(testutil.ReadFile(t, target)); got != "{}\nedited\n" {
		t.Errorf("target = %q", got)
	}
	if filepath.Dir(backup) != filepath.Dir(target) {
		t.Errorf("backup %s is not beside the real file", backup)
	}
}

func TestEditFile_retriesWhenTheAgentWritesDuringTheEdit(t *testing.T) {
	path := filepath.Join(tempDir(t), ".claude.json")
	testutil.WriteFile(t, path, "first\n")
	calls := 0
	edit := func(data []byte) ([]byte, error) {
		calls++
		if calls == 1 {
			testutil.WriteFile(t, path, "agent wrote\n")
		}
		return append(data, "mini\n"...), nil
	}

	backup, err := EditFile(path, edit, editTime)

	if err != nil {
		t.Fatal(err)
	}
	if got := string(testutil.ReadFile(t, path)); got != "agent wrote\nmini\n" || calls != 2 {
		t.Errorf("config = %q after %d attempts, want the agent's write kept and edited on attempt 2", got, calls)
	}
	if got := string(testutil.ReadFile(t, backup)); got != "agent wrote\n" {
		t.Errorf("backup = %q, want the bytes the successful attempt read", got)
	}
	requireFiles(t, filepath.Dir(path), ".claude.json", ".claude.minibackup.json")
}

func TestEditFile_givesUpWhenTheFileKeepsChanging(t *testing.T) {
	path := filepath.Join(tempDir(t), "config.toml")
	testutil.WriteFile(t, path, "0\n")
	calls := 0
	edit := func(data []byte) ([]byte, error) {
		calls++
		testutil.WriteFile(t, path, strings.Repeat("x", calls)+"\n")
		return []byte("mini\n"), nil
	}

	_, err := EditFile(path, edit, editTime)

	if !errors.Is(err, ErrConfigKeptChanging) || calls != maxEditAttempts {
		t.Fatalf("EditFile = %v after %d attempts, want ErrConfigKeptChanging after %d", err, calls, maxEditAttempts)
	}
	if got := string(testutil.ReadFile(t, path)); got != "xxx\n" {
		t.Errorf("config = %q, want the agent's last write untouched", got)
	}
	requireFiles(t, filepath.Dir(path), "config.toml")
}

func TestEditFile_neverOverwritesAnEarlierBackup(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "config.toml")
	testutil.WriteFile(t, path, "current\n")
	earlier := filepath.Join(dir, "config.minibackup.toml")
	testutil.WriteFile(t, earlier, "earlier\n")

	backup, err := EditFile(path, appendLine("edited"), editTime)

	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "config.minibackup.20261004T123045.toml"); backup != want {
		t.Errorf("backup = %s, want %s", backup, want)
	}
	if got := string(testutil.ReadFile(t, earlier)); got != "earlier\n" {
		t.Errorf("earlier backup = %q, want it untouched", got)
	}

	t.Run("a failed backup stops before the file is written", func(t *testing.T) {
		_, err := EditFile(path, appendLine("again"), editTime)
		if !errors.Is(err, os.ErrExist) {
			t.Fatalf("EditFile = %v, want the backup's exists error", err)
		}
		if got := string(testutil.ReadFile(t, path)); got != "current\nedited\n" {
			t.Errorf("config = %q, want it unchanged", got)
		}
	})
}

func TestEditFile_anEditThatChangesNothingWritesNothing(t *testing.T) {
	path := filepath.Join(tempDir(t), "config.toml")
	testutil.WriteFile(t, path, "model = \"o3\"\n")
	unchanged := func(data []byte) ([]byte, error) { return data, nil }

	backup, err := EditFile(path, unchanged, editTime)

	if err != nil || backup != "" {
		t.Fatalf("EditFile = %q, %v; want no backup and no error", backup, err)
	}
	requireFiles(t, filepath.Dir(path), "config.toml")
}

func TestEditFile_anEditErrorLeavesNothingBehind(t *testing.T) {
	path := filepath.Join(tempDir(t), "config.toml")
	testutil.WriteFile(t, path, "model = \"o3\"\n")
	refuse := func([]byte) ([]byte, error) { return nil, errors.New("synthetic refusal") }

	if _, err := EditFile(path, refuse, editTime); err == nil || err.Error() != "synthetic refusal" {
		t.Fatalf("EditFile = %v, want the edit's error", err)
	}
	requireFiles(t, filepath.Dir(path), "config.toml")
}

func TestBackupPath_keepsTheExtensionLast(t *testing.T) {
	for path, want := range map[string]string{
		"/h/.codex/config.toml": "/h/.codex/config.minibackup.toml",
		"/h/.claude.json":       "/h/.claude.minibackup.json",
		"/h/mcp_config":         "/h/mcp_config.minibackup",
	} {
		if got := backupPath(path, ""); got != want {
			t.Errorf("backupPath(%s) = %s, want %s", path, got, want)
		}
	}
}

func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", filepath.Base(path), got, want)
	}
}

func requireFiles(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s holds %v, want %v", dir, got, want)
	}
}

func TestEditFile_mutableCallbackKeepsTheOriginalBackup(t *testing.T) {
	for _, tt := range []struct {
		name string
		edit func([]byte) ([]byte, error)
		want string
	}{
		{"returns its input", func(data []byte) ([]byte, error) { copy(data, "new"); return data, nil }, "new\n"},
		{"returns separate bytes", func(data []byte) ([]byte, error) { copy(data, "new"); return []byte("replacement\n"), nil }, "replacement\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(tempDir(t), "config.toml")
			testutil.WriteFile(t, path, "old\n")
			backup, err := EditFile(path, tt.edit, editTime)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(testutil.ReadFile(t, backup)); got != "old\n" {
				t.Fatalf("backup = %q, want original bytes", got)
			}
			if got := string(testutil.ReadFile(t, path)); got != tt.want {
				t.Fatalf("config = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEditFile_retryFollowsTheCurrentSymlinkTarget(t *testing.T) {
	dir := tempDir(t)
	first, second, link := filepath.Join(dir, "first.toml"), filepath.Join(dir, "second.toml"), filepath.Join(dir, "config.toml")
	testutil.WriteFile(t, first, "first\n")
	testutil.WriteFile(t, second, "second\n")
	if err := os.Symlink(first, link); err != nil {
		t.Skip("cannot create symlink:", err)
	}
	calls := 0
	edit := func(data []byte) ([]byte, error) {
		calls++
		if calls == 1 {
			testutil.WriteFile(t, first, "agent wrote\n")
			replaceTestSymlink(t, link, second)
		}
		return append(data, "mini\n"...), nil
	}
	backup, err := EditFile(link, edit, editTime)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(testutil.ReadFile(t, link)); got != "second\nmini\n" || calls != 2 {
		t.Fatalf("config = %q after %d attempts; want second target edited on attempt 2", got, calls)
	}
	if got := string(testutil.ReadFile(t, first)); got != "agent wrote\n" {
		t.Fatalf("old target = %q, want external write untouched", got)
	}
	if backup != filepath.Join(dir, "second.minibackup.toml") || string(testutil.ReadFile(t, backup)) != "second\n" {
		t.Fatalf("backup = %q, want second target's starting bytes", backup)
	}
}

func replaceTestSymlink(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestEditFile_nilResultIsRefusedButExplicitEmptyContentIsWritten(t *testing.T) {
	for _, tt := range []struct {
		name string
		data []byte
	}{
		{"nil", nil},
		{"explicit empty", []byte{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(tempDir(t), "config.toml")
			testutil.WriteFile(t, path, "original\n")
			backup, err := EditFile(path, func([]byte) ([]byte, error) { return tt.data, nil }, editTime)
			if tt.data == nil {
				if err == nil || backup != "" || string(testutil.ReadFile(t, path)) != "original\n" {
					t.Fatalf("nil result: backup = %q, error = %v; want refusal and original bytes", backup, err)
				}
				requireFiles(t, filepath.Dir(path), "config.toml")
				return
			}
			if err != nil || backup == "" || len(testutil.ReadFile(t, path)) != 0 || string(testutil.ReadFile(t, backup)) != "original\n" {
				t.Fatalf("empty result: backup = %q, error = %v; want empty file and original backup", backup, err)
			}
		})
	}
}
