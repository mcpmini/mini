package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func TestRun_failsANewLongTestAndPassesOneInTheBaseline(t *testing.T) {
	inTempModule(t)
	testutil.WriteFile(t, "pkg/a_test.go", "package p\nfunc TestLong() {\n"+codeLines(30)+"}\n")
	testutil.WriteFile(t, "test_baseline.txt", "")

	failed, out := runIn(t, options{dirs: []string{"."}, baselinePath: "test_baseline.txt"})
	if !failed || !strings.Contains(out, "ERROR pkg/a_test.go:2: TestLong/body is 30 code lines (>= 25)") {
		t.Fatalf("without a baseline entry: failed=%v output:\n%s", failed, out)
	}

	testutil.WriteFile(t, "test_baseline.txt", "pkg:TestLong 30\n")
	if failed, out := runIn(t, options{dirs: []string{"."}, baselinePath: "test_baseline.txt"}); failed {
		t.Fatalf("with a baseline entry: output:\n%s", out)
	}
}

func TestRun_aNewOverLimitSubtestInABaselinedTestFails(t *testing.T) {
	inTempModule(t)
	subtest := func(name string, lines int) string {
		return "\tt.Run(\"" + name + "\", func(t *testing.T) {\n" + codeLines(lines) + "\t})\n"
	}
	testutil.WriteFile(t, "pkg/a_test.go", "package p\nimport \"testing\"\nfunc TestBig(t *testing.T) {\n"+
		subtest("a", 40)+subtest("b", 39)+"}\n")
	testutil.WriteFile(t, "test_baseline.txt", "pkg:TestBig 40\n")

	failed, out := runIn(t, options{dirs: []string{"."}, baselinePath: "test_baseline.txt"})

	if !failed || !strings.Contains(out, "scopes over the limit total 79 code lines, baseline allows 40") {
		t.Fatalf("failed=%v output:\n%s", failed, out)
	}
}

func TestRun_anyWayOfSpellingTheDirectoryMatchesTheBaseline(t *testing.T) {
	inTempModule(t)
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
	inTempModule(t)
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

func TestRun_anUnparseableTestFileFailsThePruneAndLeavesTheBaseline(t *testing.T) {
	inTempModule(t)
	testutil.WriteFile(t, "pkg/a_test.go", "package p\nfunc TestLong() {\n")
	testutil.WriteFile(t, "test_baseline.txt", "pkg:TestLong 40\n")

	_, err := run(options{dirs: []string{"."}, baselinePath: "test_baseline.txt", pruneBaseline: true}, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "a_test.go") {
		t.Errorf("run error = %v, want one naming the unparseable file", err)
	}
	if data := testutil.ReadFile(t, "test_baseline.txt"); string(data) != "pkg:TestLong 40\n" {
		t.Errorf("baseline = %q, want it untouched", data)
	}
}

func TestRun_aPruneFromASubdirectoryKeepsTheOtherPackagesEntries(t *testing.T) {
	inTempModule(t)
	testutil.WriteFile(t, "pkg/a_test.go", "package p\nfunc TestLong() {\n"+codeLines(30)+"}\n")
	testutil.WriteFile(t, "test_baseline.txt", "other:TestOther 30\npkg:TestLong 30\n")
	t.Chdir("pkg")

	failed, out := runIn(t, options{dirs: []string{"."}, baselinePath: "../test_baseline.txt", pruneBaseline: true})

	if failed {
		t.Fatalf("prune run failed:\n%s", out)
	}
	if data := testutil.ReadFile(t, "../test_baseline.txt"); string(data) != "other:TestOther 30\npkg:TestLong 30\n" {
		t.Errorf("baseline after prune = %q, want both entries kept", data)
	}
}

func TestRun_aMissingBaselineIsAnError(t *testing.T) {
	inTempModule(t)
	testutil.WriteFile(t, "pkg/a_test.go", "package p\n")

	if _, err := run(options{dirs: []string{"."}, baselinePath: "missing.txt"}, io.Discard); err == nil {
		t.Error("run with a missing baseline succeeded, want an error")
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

// inTempModule moves into a fresh module, so baseline keys don't depend on a go.mod above $TMPDIR.
func inTempModule(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	testutil.WriteFile(t, "go.mod", "module m\n")
}
