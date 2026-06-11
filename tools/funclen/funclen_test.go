package main

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// parseIssues parses src as a Go file and returns any funclen issues found.
func parseIssues(t *testing.T, src string) []issue {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return collectFuncIssues(f, fset, strings.Split(src, "\n"))
}

// codeLines returns n lines of valid Go statements for use as a function body.
func codeLines(n int) string {
	return strings.Repeat("\t_ = 0\n", n)
}

// src builds a complete Go file with a single top-level function whose body
// contains the given lines. The function declaration is always on line 2.
func src(body string) string {
	return "package p\nfunc f() {\n" + body + "}\n"
}

func TestThresholds(t *testing.T) {
	t.Run("below warning: no issue", func(t *testing.T) {
		// 14 body lines < warnAt(15) — func/} lines excluded from count
		issues := parseIssues(t, src(codeLines(14)))
		if len(issues) != 0 {
			t.Fatalf("want no issues, got %+v", issues)
		}
	})

	t.Run("at warning threshold: warning", func(t *testing.T) {
		// 15 body lines = warnAt
		issues := parseIssues(t, src(codeLines(15)))
		if len(issues) != 1 {
			t.Fatalf("want 1 issue, got %+v", issues)
		}
		if issues[0].isError {
			t.Error("want WARNING, got ERROR")
		}
		if issues[0].lines != 15 {
			t.Errorf("want lines=15, got %d", issues[0].lines)
		}
		if issues[0].name != "f" {
			t.Errorf("want name=f, got %q", issues[0].name)
		}
	})

	t.Run("at error threshold: error", func(t *testing.T) {
		// 25 body lines = errorAt
		issues := parseIssues(t, src(codeLines(25)))
		if len(issues) != 1 {
			t.Fatalf("want 1 issue, got %+v", issues)
		}
		if !issues[0].isError {
			t.Error("want ERROR, got WARNING")
		}
		if issues[0].lines != 25 {
			t.Errorf("want lines=25, got %d", issues[0].lines)
		}
	})
}

func TestCommentExclusion(t *testing.T) {
	t.Run("line comments excluded", func(t *testing.T) {
		// 13 code + 2 comments = 15 body span, but only 13 code lines → no issue
		body := codeLines(13) + "\t// first comment\n\t// second comment\n"
		issues := parseIssues(t, src(body))
		if len(issues) != 0 {
			t.Fatalf("want no issues (comments excluded), got %+v", issues)
		}
	})

	t.Run("block comment excluded", func(t *testing.T) {
		// 13 code + 3-line block comment = 16 body span, 13 code lines → no issue
		body := codeLines(13) + "\t/*\n\t   invariant\n\t*/\n"
		issues := parseIssues(t, src(body))
		if len(issues) != 0 {
			t.Fatalf("want no issues (block comment excluded), got %+v", issues)
		}
	})

	t.Run("single-line block comment excluded", func(t *testing.T) {
		body := codeLines(13) + "\t/* inline block */\n\t/* another */\n"
		issues := parseIssues(t, src(body))
		if len(issues) != 0 {
			t.Fatalf("want no issues (single-line block comments excluded), got %+v", issues)
		}
	})

	t.Run("trailing inline comment counts as code", func(t *testing.T) {
		// `_ = 0 // note` starts with code, not a comment — must count
		body := strings.Repeat("\t_ = 0 // note\n", 15)
		issues := parseIssues(t, src(body))
		if len(issues) != 1 {
			t.Fatalf("want 1 issue (trailing comment is code), got %+v", issues)
		}
	})
}

func TestNolint(t *testing.T) {
	t.Run("//nolint suppresses issue", func(t *testing.T) {
		s := "package p\nfunc f() { //nolint\n" + codeLines(15) + "}\n"
		issues := parseIssues(t, s)
		if len(issues) != 0 {
			t.Fatalf("want no issues with //nolint, got %+v", issues)
		}
	})

	t.Run("//nolint:funclen suppresses issue", func(t *testing.T) {
		s := "package p\nfunc f() { //nolint:funclen\n" + codeLines(15) + "}\n"
		issues := parseIssues(t, s)
		if len(issues) != 0 {
			t.Fatalf("want no issues with //nolint:funclen, got %+v", issues)
		}
	})

	t.Run("nolint in body does not suppress", func(t *testing.T) {
		body := codeLines(14) + "\t_ = 0 //nolint\n"
		issues := parseIssues(t, src(body))
		if len(issues) != 1 {
			t.Fatalf("want 1 issue (nolint in body ignored), got %+v", issues)
		}
	})
}

func TestReceiverNaming(t *testing.T) {
	t.Run("value receiver", func(t *testing.T) {
		s := "package p\ntype T struct{}\nfunc (t T) M() {\n" + codeLines(15) + "}\n"
		issues := parseIssues(t, s)
		if len(issues) != 1 {
			t.Fatalf("want 1 issue, got %+v", issues)
		}
		if issues[0].name != "T.M" {
			t.Errorf("want T.M, got %q", issues[0].name)
		}
	})

	t.Run("pointer receiver", func(t *testing.T) {
		s := "package p\ntype T struct{}\nfunc (t *T) M() {\n" + codeLines(15) + "}\n"
		issues := parseIssues(t, s)
		if len(issues) != 1 {
			t.Fatalf("want 1 issue, got %+v", issues)
		}
		if issues[0].name != "T.M" {
			t.Errorf("want T.M, got %q", issues[0].name)
		}
	})
}
