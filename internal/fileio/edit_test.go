package fileio

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func appendLine(line string) func([]byte) ([]byte, error) {
	return func(data []byte) ([]byte, error) { return append(data, line+"\n"...), nil }
}

func TestEditFile_editsTheFileAndKeepsItsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	testutil.WriteFile(t, path, "first\n")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	changed, err := EditFile(EditParams{Path: path, Edit: appendLine("mini")})

	if err != nil || !changed {
		t.Fatalf("EditFile = %v, %v; want changed", changed, err)
	}
	if got := string(testutil.ReadFile(t, path)); got != "first\nmini\n" {
		t.Errorf("file = %q", got)
	}
	assertMode(t, path, 0o640)
}

func TestEditFile_writesNothingUnlessTheEditChangesTheBytes(t *testing.T) {
	cases := map[string]struct {
		edit    func([]byte) ([]byte, error)
		wantErr bool
	}{
		"returns them unchanged": {edit: func(data []byte) ([]byte, error) { return data, nil }},
		"returns an error": {
			edit:    func([]byte) ([]byte, error) { return nil, errors.New("refused") },
			wantErr: true,
		},
		"returns nil": {edit: func([]byte) ([]byte, error) { return nil, nil }, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			testutil.WriteFile(t, path, "original\n")
			hookRan := false

			changed, err := EditFile(
				EditParams{Path: path, Edit: tc.edit, BeforeReplace: func(string, []byte) (func(), error) {
					hookRan = true
					return nil, nil
				}},
			)

			if changed || (err != nil) != tc.wantErr || hookRan {
				t.Fatalf(
					"EditFile = %v, %v, hook ran %v; want unchanged, error %v, no hook",
					changed,
					err,
					hookRan,
					tc.wantErr,
				)
			}
			if got := string(testutil.ReadFile(t, path)); got != "original\n" {
				t.Errorf("file = %q, want it untouched", got)
			}
			assertOnlyEntries(t, dir, path)
		})
	}
}

func TestEditFile_writesAnExplicitlyEmptyResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	testutil.WriteFile(t, path, "original\n")

	changed, err := EditFile(EditParams{Path: path, Edit: func([]byte) ([]byte, error) { return []byte{}, nil }})

	if err != nil || !changed || len(testutil.ReadFile(t, path)) != 0 {
		t.Fatalf("EditFile = %v, %v; want an empty file", changed, err)
	}
}

func TestEditFile_retriesWhenTheFileChangesDuringTheEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	testutil.WriteFile(t, path, "first\n")
	calls := 0
	edit := func(data []byte) ([]byte, error) {
		calls++
		if calls == 1 {
			testutil.WriteFile(t, path, "someone else\n")
		}
		return append(data, "mini\n"...), nil
	}

	if _, err := EditFile(EditParams{Path: path, Edit: edit}); err != nil {
		t.Fatal(err)
	}

	if got := string(testutil.ReadFile(t, path)); got != "someone else\nmini\n" || calls != 2 {
		t.Errorf("file = %q after %d edits, want the other write kept and edited on the second", got, calls)
	}
}

func TestEditFile_givesUpWhenTheFileKeepsChanging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	testutil.WriteFile(t, path, "0\n")
	calls := 0
	edit := func([]byte) ([]byte, error) {
		calls++
		testutil.WriteFile(t, path, strings.Repeat("x", calls)+"\n")
		return []byte("mini\n"), nil
	}

	_, err := EditFile(EditParams{Path: path, Edit: edit})

	if !errors.Is(err, ErrKeptChanging) || calls != editAttempts {
		t.Fatalf("EditFile = %v after %d edits, want ErrKeptChanging after %d", err, calls, editAttempts)
	}
	if got := string(testutil.ReadFile(t, path)); got != "xxx\n" {
		t.Errorf("file = %q, want the last other write untouched", got)
	}
	assertOnlyEntries(t, dir, path)
}

func TestEditFile_followsTheSymlinkWhereItPointsNow(t *testing.T) {
	dir := t.TempDir()
	first, second, link := filepath.Join(
		dir,
		"first.toml",
	), filepath.Join(
		dir,
		"second.toml",
	), filepath.Join(
		dir,
		"config.toml",
	)
	testutil.WriteFile(t, first, "first\n")
	testutil.WriteFile(t, second, "second\n")
	if err := os.Symlink(first, link); err != nil {
		t.Skip("cannot create symlink:", err)
	}
	calls := 0
	edit := func(data []byte) ([]byte, error) {
		calls++
		if calls == 1 {
			pointSymlink(t, link, second)
		}
		return append(data, "mini\n"...), nil
	}

	if _, err := EditFile(EditParams{Path: link, Edit: edit}); err != nil {
		t.Fatal(err)
	}

	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link replaced by a regular file: %v", err)
	}
	if got := string(testutil.ReadFile(t, second)); got != "second\nmini\n" {
		t.Errorf("new target = %q, want it edited", got)
	}
	if got := string(testutil.ReadFile(t, first)); got != "first\n" {
		t.Errorf("old target = %q, want it untouched", got)
	}
}

func TestEditFile_doesNotRecreateAFileRemovedDuringTheEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	testutil.WriteFile(t, path, "first\n")
	edit := func(data []byte) ([]byte, error) {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return append(data, "mini\n"...), nil
	}

	_, err := EditFile(EditParams{Path: path, Edit: edit})

	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("EditFile = %v, want not exist", err)
	}
	assertOnlyEntries(t, dir)
}

func TestEditFile_beforeReplace(t *testing.T) {
	t.Run("an error stops the edit before the file is written", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		testutil.WriteFile(t, path, "first\n")
		refusal := errors.New("no backup")

		_, err := EditFile(
			EditParams{Path: path, Edit: appendLine("mini"), BeforeReplace: func(string, []byte) (func(), error) {
				return nil, refusal
			}},
		)

		if !errors.Is(err, refusal) || string(testutil.ReadFile(t, path)) != "first\n" {
			t.Fatalf(
				"EditFile = %v, file %q; want the hook's error and the file untouched",
				err,
				testutil.ReadFile(t, path),
			)
		}
	})

	t.Run("gets the bytes it read, and its undo runs when the replace fails", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		testutil.WriteFile(t, path, "first\n")
		var seen string
		undone := false
		edit := func(data []byte) ([]byte, error) {
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(
				func() { os.Chmod(dir, 0o700) },
			) //nolint:errcheck // TempDir cleanup reports a directory it can't remove
			return append(data, "mini\n"...), nil
		}

		_, err := EditFile(
			EditParams{Path: path, Edit: edit, BeforeReplace: func(_ string, original []byte) (func(), error) {
				seen = string(original)
				return func() { undone = true }, nil
			}},
		)

		if err == nil || seen != "first\n" || !undone {
			t.Fatalf(
				"EditFile = %v, hook saw %q, undone %v; want a replace error, the original bytes, and the undo",
				err,
				seen,
				undone,
			)
		}
	})
}

func pointSymlink(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
