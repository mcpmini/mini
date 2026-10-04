package fileio

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func TestReplaceFile_writesBytesAndAppliesMode(t *testing.T) {
	for _, perm := range []os.FileMode{0600, 0644} {
		t.Run(perm.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "target")
			testutil.WriteFile(t, path, "old")
			if err := ReplaceFile(path, []byte("new"), ReplaceOptions{Perm: perm}); err != nil {
				t.Fatalf("ReplaceFile: %v", err)
			}
			if got := string(testutil.ReadFile(t, path)); got != "new" {
				t.Fatalf("contents = %q, want %q", got, "new")
			}
			assertMode(t, path, perm)
		})
	}
}

func TestReplaceFile_replacesSymlinkAndPreservesTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "link")
	target := filepath.Join(dir, "target")
	testutil.WriteFile(t, target, "linked")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := ReplaceFile(path, []byte("replacement"), ReplaceOptions{Perm: 0600}); err != nil {
		t.Fatalf("ReplaceFile: %v", err)
	}
	if got := string(testutil.ReadFile(t, path)); got != "replacement" {
		t.Fatalf("link contents = %q", got)
	}
	if got := string(testutil.ReadFile(t, target)); got != "linked" {
		t.Fatalf("target contents = %q", got)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("destination is still a symlink: info=%v err=%v", info, err)
	}
}

func TestReplaceFile_hookRunsAfterStagingAndBeforeRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	testutil.WriteFile(t, path, "old")
	calls := 0
	err := ReplaceFile(path, []byte("new"), ReplaceOptions{
		Perm: 0644,
		BeforeRename: func() error {
			calls++
			if got := string(testutil.ReadFile(t, path)); got != "old" {
				t.Fatalf("destination in hook = %q", got)
			}
			staged := otherPaths(t, dir, path)
			if len(staged) != 1 || string(testutil.ReadFile(t, staged[0])) != "new" {
				t.Fatalf("staged files = %v", staged)
			}
			assertMode(t, staged[0], 0644)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ReplaceFile: %v", err)
	}
	if calls != 1 || string(testutil.ReadFile(t, path)) != "new" {
		t.Fatalf("calls=%d contents=%q", calls, testutil.ReadFile(t, path))
	}
}

func TestReplaceFile_hookErrorPreservesDestinationAndCleansStage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	testutil.WriteFile(t, path, "old")
	sentinel := errors.New("check failed")
	err := ReplaceFile(path, []byte("new"), ReplaceOptions{
		Perm: 0600, BeforeRename: func() error { return sentinel },
	})
	if err != sentinel || !errors.Is(err, sentinel) { //nolint:errorlint // identity is part of the callback contract
		t.Fatalf("error = %v, want identical sentinel", err)
	}
	if got := string(testutil.ReadFile(t, path)); got != "old" {
		t.Fatalf("destination = %q", got)
	}
	assertOnlyEntries(t, dir, path)
}

func TestReplaceFile_failedRenamePreservesDirectoryAndCleansStage(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "nonempty"}[populated], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "destination")
			if populated {
				testutil.WriteFile(t, filepath.Join(path, "keep"), "contents")
			} else if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			if err := ReplaceFile(path, []byte("new"), ReplaceOptions{Perm: 0600}); err == nil {
				t.Fatal("ReplaceFile succeeded over a directory")
			}
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				t.Fatalf("destination directory = %v, err=%v", info, err)
			}
			if populated && string(testutil.ReadFile(t, filepath.Join(path, "keep"))) != "contents" {
				t.Fatal("directory contents changed")
			}
			assertOnlyEntries(t, dir, path)
		})
	}
}

func TestReplaceFile_stagingErrorClosesAndCleansStage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	testutil.WriteFile(t, path, "old")
	sentinel := errors.New("write failed")
	closed, hookCalled := false, false
	p := replaceParams{
		path: path, data: []byte("new"), opts: ReplaceOptions{Perm: 0600, BeforeRename: func() error {
			hookCalled = true
			return nil
		}},
		createTemp: func(dir, pattern string) (file, error) {
			f, err := os.CreateTemp(dir, pattern)
			return failedFile{file: f, writeErr: sentinel, onClose: func() { closed = true }}, err
		},
	}
	if err := replaceFile(p); !errors.Is(err, sentinel) {
		t.Fatalf("replaceFile error = %v", err)
	}
	if !closed || hookCalled {
		t.Fatalf("closed=%v hookCalled=%v", closed, hookCalled)
	}
	if got := string(testutil.ReadFile(t, path)); got != "old" {
		t.Fatalf("destination = %q", got)
	}
	assertOnlyEntries(t, dir, path)
}

func TestCreateFile_exclusiveCreationAndCollisionClassification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target")
	if err := CreateFile(path, []byte("new"), 0600); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	if got := string(testutil.ReadFile(t, path)); got != "new" {
		t.Fatalf("contents = %q", got)
	}
	err := CreateFile(path, []byte("overwrite"), 0600)
	if !os.IsExist(err) || !errors.Is(err, fs.ErrExist) {
		t.Fatalf("collision error = %v", err)
	}
	if got := string(testutil.ReadFile(t, path)); got != "new" {
		t.Fatalf("collision changed contents to %q", got)
	}
}

func TestCreateFile_collisionLeavesSymlinkAndTargetUntouched(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	path := filepath.Join(dir, "link")
	testutil.WriteFile(t, target, "linked")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := CreateFile(path, []byte("replacement"), 0600); !os.IsExist(err) {
		t.Fatalf("CreateFile collision error = %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("destination symlink changed: info=%v err=%v", info, err)
	}
	if got := string(testutil.ReadFile(t, target)); got != "linked" {
		t.Fatalf("target contents = %q", got)
	}
}

func TestCreateFile_writeAndCloseErrorsRemovePartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target")
	writeErr, closeErr := errors.New("write failed"), errors.New("close failed")
	closed := false
	p := createParams{
		path: path, data: []byte("partial"), perm: 0600,
		open: func(path string, flags int, perm os.FileMode) (file, error) {
			f, err := os.OpenFile(path, flags, perm)
			return failedFile{file: f, writeErr: writeErr, closeErr: closeErr, onClose: func() { closed = true }}, err
		},
	}
	err := createFile(p)
	if !errors.Is(err, writeErr) || !errors.Is(err, closeErr) || !closed {
		t.Fatalf("error = %v, closed=%v", err, closed)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("partial file remains: %v", err)
	}
	if err := CreateFile(path, []byte("complete"), 0600); err != nil {
		t.Fatalf("later create: %v", err)
	}
}

func TestCreateFile_openErrorIsReturnedUnchanged(t *testing.T) {
	sentinel := errors.New("open failed")
	err := createFile(createParams{
		path: "unused", open: func(string, int, os.FileMode) (file, error) { return nil, sentinel },
	})
	if err != sentinel { //nolint:errorlint // open errors must pass through unchanged
		t.Fatalf("error = %v, want identical sentinel", err)
	}
}

func TestFileOperations_doNotCreateMissingParents(t *testing.T) {
	for _, replace := range []bool{true, false} {
		t.Run(map[bool]string{true: "replace", false: "create"}[replace], func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "missing")
			path := filepath.Join(parent, "target")
			var err error
			if replace {
				err = ReplaceFile(path, []byte("new"), ReplaceOptions{Perm: 0600})
			} else {
				err = CreateFile(path, []byte("new"), 0600)
			}
			if err == nil {
				t.Fatal("file operation succeeded with missing parent")
			}
			if _, statErr := os.Stat(parent); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("parent exists or stat failed unexpectedly: %v", statErr)
			}
		})
	}
}

type failedFile struct {
	file
	writeErr error
	closeErr error
	onClose  func()
}

func (f failedFile) Write(data []byte) (int, error) {
	n, _ := f.file.Write(data)
	return n, f.writeErr
}

func (f failedFile) Close() error {
	err := f.file.Close()
	if f.onClose != nil {
		f.onClose()
	}
	return errors.Join(err, f.closeErr)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode = %#o, want %#o", got, want)
	}
}

func otherPaths(t *testing.T, dir string, expected ...string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	expectedNames := make(map[string]bool, len(expected))
	for _, path := range expected {
		expectedNames[filepath.Base(path)] = true
	}
	var paths []string
	for _, entry := range entries {
		if !expectedNames[entry.Name()] {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	return paths
}

func assertOnlyEntries(t *testing.T, dir string, expected ...string) {
	t.Helper()
	if paths := otherPaths(t, dir, expected...); len(paths) != 0 {
		t.Fatalf("unexpected files remain: %v", paths)
	}
}
