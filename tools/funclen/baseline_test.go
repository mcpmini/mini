package main

import (
	"go/token"
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func measured(lines int) testFunc {
	f := testFunc{name: "TestX", path: "pkg/x_test.go", longest: scope{name: "body", line: 3, lines: lines}}
	if lines >= errorAt {
		f.overLimitTotal = lines
	}
	return f
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
			wantPart: "scopes over the limit total 41 code lines, baseline allows 40; shorten them (see docs/testing.md)",
		},
		{
			name: "listed test shrank", entries: map[string]int{"pkg:TestX": 40}, lines: 30,
			wantPart: "scopes over the limit now total 30 lines, baseline allows 40; run go run ./tools/funclen -prune-baseline",
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
	inTempModule(t)
	b := baselineOf(map[string]int{"gone/pkg:TestOld": 30})

	assertProblems(t, b.check(nil, inWalkedDirs([]string{"."})), "gone/pkg:TestOld matches no function")
	assertProblems(t, b.check(nil, inWalkedDirs([]string{"other"})), "")
}

func TestBaseline_pruneLowersAndRemovesButNeverAddsOrRaises(t *testing.T) {
	inTempModule(t)
	b := baselineOf(map[string]int{
		"pkg:TestShrank": 40, "pkg:TestFixed": 40, "pkg:TestGone": 40, "pkg:TestGrew": 40, "out:TestKept": 40,
	})
	funcs := map[string]testFunc{
		"pkg:TestShrank": measured(30), "pkg:TestFixed": measured(24), "pkg:TestGrew": measured(50),
		"pkg:TestNew": measured(60),
	}

	b.prune(funcs, inWalkedDirs([]string{"pkg"}))

	want := map[string]int{"pkg:TestShrank": 30, "pkg:TestGrew": 40, "out:TestKept": 40}
	if !maps.Equal(b.entries, want) {
		t.Errorf("entries after prune = %v, want %v", b.entries, want)
	}
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
	if err != nil || !maps.Equal(loaded.entries, b.entries) {
		t.Errorf("loadBaseline = %v, %v; want %v", loaded.entries, err, b.entries)
	}
}

func TestBaseline_aMalformedLineNamesItsPosition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test_baseline.txt")
	for _, bad := range []string{"no-count", "pkg:TestB many"} {
		testutil.WriteFile(t, path, "pkg:TestA 26\n"+bad+"\n")

		_, err := loadBaseline(path)
		if err == nil || !strings.Contains(err.Error(), "test_baseline.txt:2: want \"pkgdir:FuncName N\"") {
			t.Errorf("loadBaseline(%q) error = %v, want one naming line 2 and the format", bad, err)
		}
	}
}

func TestMeasureTests_aNameRepeatedInOneDirectoryIsJudgedAsOne(t *testing.T) {
	inTempModule(t)
	testutil.WriteFile(t, "pkg/a_test.go", "package p\nfunc helper() {\n"+codeLines(26)+"}\n")
	testutil.WriteFile(t, "pkg/b_test.go", "package p_test\nfunc helper() {\n"+codeLines(30)+"}\n")

	funcs, err := measureTests([]string{"pkg/a_test.go", "pkg/b_test.go"}, token.NewFileSet())
	if err != nil {
		t.Fatal(err)
	}

	if got := funcs["pkg:helper"]; got.overLimitTotal != 56 || got.path != "pkg/b_test.go" || got.longest.lines != 30 {
		t.Errorf("helper = %+v, want both copies' 56 lines, reported at the longer copy in b_test.go", got)
	}
}
