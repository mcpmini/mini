package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func TestWalk_skipsDotAndTestdataDirectoriesBelowTheRootButNotTheRoot(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, path := range []string{
		"pkg/a_test.go", ".claude/worktrees/copy/pkg/a_test.go", ".agents/wt/b_test.go", "vendor/v/c_test.go",
		"pkg/testdata/broken_test.go",
	} {
		testutil.WriteFile(t, path, "package p\n")
	}

	files, err := collectFiles([]string{"."})
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"pkg/a_test.go"}; !slices.Equal(files.tests, want) {
		t.Errorf("tests = %v, want %v", files.tests, want)
	}
}

func TestWalk_aDotDirectoryGivenAsTheRootIsWalked(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".claude")
	testutil.WriteFile(t, filepath.Join(root, "pkg", "a_test.go"), "package p\n")

	files, err := collectFiles([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	if len(files.tests) != 1 {
		t.Errorf("tests = %v, want the file under the explicit dot-directory root", files.tests)
	}
}
