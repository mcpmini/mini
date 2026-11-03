package main

import (
	"go/token"
	"strings"
	"testing"
)

func testSource(t *testing.T, src string) *sourceFile {
	t.Helper()
	s, err := parseSource("x_test.go", []byte(src), token.NewFileSet())
	if err != nil {
		t.Fatalf("parse failed: %v\n%s", err, src)
	}
	return s
}

func longestOf(t *testing.T, src string) scope {
	t.Helper()
	funcs := testSource(t, src).testFuncs()
	if len(funcs) != 1 {
		t.Fatalf("want one function, got %d", len(funcs))
	}
	return funcs[0].longest
}

func subtestSrc(name string, parentLines, childLines int) string {
	return "package p\nimport \"testing\"\nfunc TestX(t *testing.T) {\n" + codeLines(parentLines) +
		"\tt.Run(" + name + ", func(t *testing.T) {\n" + codeLines(childLines) + "\t})\n}\n"
}

func TestScopes_aSubtestIsMeasuredOnItsOwn(t *testing.T) {
	scopes := testSource(t, subtestSrc(`"child"`, 3, 30)).testFuncs()[0]

	if got := scopes.longest; got.name != "child" || got.lines != 30 || got.line != 7 {
		t.Errorf("longest = %+v, want child at line 7 with 30 lines", got)
	}
}

func TestScopes_theParentCountsOnlyItsOwnLines(t *testing.T) {
	src := subtestSrc(`"child"`, 26, 2)

	if got := longestOf(t, src); got.name != "body" || got.lines != 28 {
		t.Errorf("longest = %+v, want body with its 26 lines plus the t.Run call and its close", got)
	}
}

func TestScopes_nestedSubtestsJoinTheirNames(t *testing.T) {
	src := "package p\nimport \"testing\"\nfunc TestX(t *testing.T) {\n" +
		"\tt.Run(\"outer\", func(t *testing.T) {\n\t\tt.Run(\"inner\", func(t *testing.T) {\n" +
		codeLines(30) + "\t\t})\n\t})\n}\n"

	if got := longestOf(t, src); got.name != "outer/inner" || got.lines != 30 {
		t.Errorf("longest = %+v, want outer/inner with 30 lines", got)
	}
}

func TestScopes_aComputedSubtestNameShowsItsLine(t *testing.T) {
	if got := longestOf(t, subtestSrc("tt.name", 0, 30)); got.name != "t.Run@4" {
		t.Errorf("longest = %+v, want t.Run@4", got)
	}
}

func TestScopes_aRunWhoseFuncIsNotATestStaysInTheParent(t *testing.T) {
	src := "package p\nfunc TestX() {\n\tg.Run(\"x\", func(ctx int) {\n" + codeLines(30) + "\t})\n}\n"

	if got := longestOf(t, src); got.name != "body" || got.lines != 32 {
		t.Errorf("longest = %+v, want body with all 32 lines", got)
	}
}

func TestTables_rowsDoNotCountHoweverTheTableIsUsed(t *testing.T) {
	rows := strings.Repeat("\t\t{name: \"a\"},\n", 30)
	for name, body := range map[string]string{
		"local":         "\ttests := []struct{ name string }{\n" + rows + "\t}\n\t_ = tests\n",
		"helper arg":    "\tcheck(t, []tc{\n" + rows + "\t})\n",
		"ranged":        "\tfor _, tt := range []tc{\n" + rows + "\t} {\n\t\t_ = tt\n\t}\n",
		"map of struct": "\t_ = map[string]tc{\n" + strings.Repeat("\t\t\"k\": {},\n", 30) + "\t}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := longestOf(t, src(body)); got.lines >= errorAt {
				t.Errorf("longest = %+v, want the table rows excluded", got)
			}
		})
	}
}

func callbackTableSrc(callbackBody string) string {
	return "package p\nimport \"testing\"\nfunc TestX(t *testing.T) {\n\tfor _, tc := range []tc{\n" +
		"\t\t{name: \"long\", run: func(t *testing.T) {\n" + callbackBody + "\t\t}},\n" +
		"\t\t{name: \"short\", run: func(t *testing.T) {}},\n\t} {\n\t\tt.Run(tc.name, tc.run)\n\t}\n}\n"
}

func TestTables_aCallbackInARowIsMeasuredOnItsOwn(t *testing.T) {
	if got := longestOf(t, callbackTableSrc(codeLines(30))); got.name != "func@5" || got.lines != 30 {
		t.Errorf("longest = %+v, want the row's callback at line 5 with 30 lines", got)
	}
}

func TestTables_aSubtestInARowCallbackIsMeasuredOnce(t *testing.T) {
	inner := "\t\t\tt.Run(\"inner\", func(t *testing.T) {\n" + codeLines(30) + "\t\t\t})\n"

	funcs := testSource(t, callbackTableSrc(inner)).testFuncs()

	if got := funcs[0].longest; got.name != "func@5/inner" || got.lines != 30 {
		t.Errorf("longest = %+v, want func@5/inner with 30 lines", got)
	}
}

func TestTables_aStructLiteralIsSetupAndCountsEvenWithNestedLiterals(t *testing.T) {
	fields := "\t\tAuth: &config.AuthConfig{},\n\t\tLimits: Limits{},\n" + strings.Repeat("\t\tName: \"a\",\n", 28)
	body := "\tcfg := config.ServerConfig{\n" + fields + "\t}\n\t_ = cfg\n"

	if got := longestOf(t, src(body)); got.lines < 30 {
		t.Errorf("longest = %+v, want the struct's 30 field lines counted", got)
	}
}

func TestTables_aSliceOfScalarsCounts(t *testing.T) {
	body := "\tnames := []string{\n" + strings.Repeat("\t\t\"a\",\n", 30) + "\t}\n\t_ = names\n"

	if got := longestOf(t, src(body)); got.lines < 30 {
		t.Errorf("longest = %+v, want the 30 scalar lines counted", got)
	}
}
