package main

import (
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func measured(lines int) testFunc {
	return testFunc{name: "TestX", path: "pkg/x_test.go", longest: scope{name: "body", line: 3, lines: lines}}
}

func baselineOf(entries map[string]int) baseline {
	return baseline{path: "test_baseline.txt", entries: entries}
}

func everywhere(string) bool { return true }

func TestBaseline_judgesEachTestAgainstItsEntry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		entries  map[string]int
		lines    int
		wantPart string
	}{
		{name: "new test under the limit", entries: map[string]int{}, lines: 24},
		{
			name: "new test over the limit", entries: map[string]int{}, lines: 25,
			wantPart: "pkg/x_test.go:3: TestX/body is 25 code lines (>= 25)",
		},
		{name: "listed test at its allowance", entries: map[string]int{"pkg:TestX": 40}, lines: 40},
		{
			name: "listed test grew", entries: map[string]int{"pkg:TestX": 40}, lines: 41,
			wantPart: "is 41 code lines, baseline allows 40; shorten it (see docs/testing.md)",
		},
		{
			name: "listed test shrank", entries: map[string]int{"pkg:TestX": 40}, lines: 30,
			wantPart: "is now 30 lines, baseline allows 40; run go run ./tools/funclen -prune-baseline",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := baselineOf(tc.entries).check(map[string]testFunc{"pkg:TestX": measured(tc.lines)}, everywhere)
			assertProblems(t, problems, tc.wantPart)
		})
	}
}

func assertProblems(t *testing.T, problems []string, wantPart string) {
	t.Helper()
	if wantPart == "" {
		if len(problems) != 0 {
			t.Fatalf("problems = %q, want none", problems)
		}
		return
	}
	if len(problems) != 1 || !strings.Contains(problems[0], wantPart) {
		t.Fatalf("problems = %q, want one containing %q", problems, wantPart)
	}
}

func TestBaseline_aStaleEntryFailsOnlyWhenItsPackageWasWalked(t *testing.T) {
	b := baselineOf(map[string]int{"gone/pkg:TestOld": 30})

	assertProblems(t, b.check(nil, runScope([]string{"."})), "gone/pkg:TestOld matches no function")
	assertProblems(t, b.check(nil, runScope([]string{"other"})), "")
}

func TestBaseline_pruneLowersAndRemovesButNeverAddsOrRaises(t *testing.T) {
	b := baselineOf(map[string]int{
		"pkg:TestShrank": 40, "pkg:TestFixed": 40, "pkg:TestGone": 40, "pkg:TestGrew": 40, "out:TestKept": 40,
	})
	funcs := map[string]testFunc{
		"pkg:TestShrank": measured(30), "pkg:TestFixed": measured(24), "pkg:TestGrew": measured(50),
		"pkg:TestNew": measured(60),
	}

	b.prune(funcs, runScope([]string{"pkg"}))

	want := map[string]int{"pkg:TestShrank": 30, "pkg:TestGrew": 40, "out:TestKept": 40}
	if !mapsEqual(b.entries, want) {
		t.Errorf("entries after prune = %v, want %v", b.entries, want)
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestBaseline_roundTripsThroughItsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test_baseline.txt")
	b := baseline{path: path, entries: map[string]int{"b/pkg:TestB": 30, "a/pkg:TestA": 26}}

	if err := b.write(); err != nil {
		t.Fatal(err)
	}
	if data, want := testutil.ReadFile(t, path), "a/pkg:TestA 26\nb/pkg:TestB 30\n"; string(data) != want {
		t.Errorf("file = %q, want sorted entries %q", data, want)
	}
	loaded, err := loadBaseline(path)
	if err != nil || !mapsEqual(loaded.entries, b.entries) {
		t.Errorf("loadBaseline = %v, %v; want %v", loaded.entries, err, b.entries)
	}
}

func TestBaseline_aMalformedLineNamesItsPosition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test_baseline.txt")
	testutil.WriteFile(t, path, "pkg:TestA 26\nno-count\n")

	if _, err := loadBaseline(path); err == nil || !strings.Contains(err.Error(), "test_baseline.txt:2:") {
		t.Errorf("loadBaseline error = %v, want one naming line 2", err)
	}
}

func TestMeasureTests_aNameRepeatedInOneDirectoryKeepsTheLonger(t *testing.T) {
	t.Chdir(t.TempDir())
	testutil.WriteFile(t, "pkg/a_test.go", "package p\nfunc helper() {\n"+codeLines(30)+"}\n")
	testutil.WriteFile(t, "pkg/b_test.go", "package p_test\nfunc helper() {\n"+codeLines(26)+"}\n")

	funcs := measureTests([]string{"pkg/a_test.go", "pkg/b_test.go"}, token.NewFileSet())

	if got := funcs["pkg:helper"].longest.lines; got != 30 {
		t.Errorf("helper measures %d, want the longer copy's 30", got)
	}
}
