package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func TestWalk_skipsDotDirectoriesBelowTheRootButNotTheRoot(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, path := range []string{
		"pkg/a_test.go", ".claude/worktrees/copy/pkg/a_test.go", ".agents/wt/b_test.go", "vendor/v/c_test.go",
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

func TestRun_failsANewLongTestAndPassesOneInTheBaseline(t *testing.T) {
	t.Chdir(t.TempDir())
	testutil.WriteFile(t, "pkg/a_test.go", "package p\nfunc TestLong() {\n"+codeLines(30)+"}\n")

	failed, out := runIn(t, options{dirs: []string{"."}, baselinePath: "test_baseline.txt"})
	if !failed || !strings.Contains(out, "ERROR pkg/a_test.go:2: TestLong/body is 30 code lines (>= 25)") {
		t.Fatalf("without a baseline entry: failed=%v output:\n%s", failed, out)
	}

	testutil.WriteFile(t, "test_baseline.txt", "pkg:TestLong 30\n")
	if failed, out := runIn(t, options{dirs: []string{"."}, baselinePath: "test_baseline.txt"}); failed {
		t.Fatalf("with a baseline entry: output:\n%s", out)
	}
}

func TestRun_anyWayOfSpellingTheDirectoryMatchesTheBaseline(t *testing.T) {
	t.Chdir(t.TempDir())
	testutil.WriteFile(t, "pkg/a_test.go", "package p\nfunc TestLong() {\n"+codeLines(30)+"}\n")
	testutil.WriteFile(t, "test_baseline.txt", "pkg:TestLong 30\n")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{"pkg", "./pkg", filepath.Join(wd, "pkg"), filepath.Join("..", filepath.Base(wd), "pkg")} {
		if failed, out := runIn(t, options{dirs: []string{dir}, baselinePath: "test_baseline.txt"}); failed {
			t.Errorf("run on %s failed:\n%s", dir, out)
		}
	}
}

func TestRun_pruneRewritesTheBaselineAndThenPasses(t *testing.T) {
	t.Chdir(t.TempDir())
	testutil.WriteFile(t, "pkg/a_test.go", "package p\nfunc TestLong() {\n"+codeLines(28)+"}\n")
	testutil.WriteFile(t, "test_baseline.txt", "pkg:TestLong 40\npkg:TestGone 30\n")

	failed, out := runIn(t, options{dirs: []string{"."}, baselinePath: "test_baseline.txt", pruneBaseline: true})

	if failed {
		t.Fatalf("prune run failed:\n%s", out)
	}
	if data := testutil.ReadFile(t, "test_baseline.txt"); string(data) != "pkg:TestLong 28\n" {
		t.Errorf("baseline after prune = %q, want only TestLong at 28", data)
	}
}

func runIn(t *testing.T, opts options) (failed bool, output string) {
	t.Helper()
	var out bytes.Buffer
	failed, err := run(opts, &out)
	if err != nil {
		t.Fatal(err)
	}
	return failed, out.String()
}
