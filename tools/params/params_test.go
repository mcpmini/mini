package main

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func parseIssues(t *testing.T, src string) []issue {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return collectFuncIssues(f, fset, strings.Split(src, "\n"))
}

func fourParamFunc() string {
	return "func f(a, b, c, d int) {}\n"
}

func TestThresholds(t *testing.T) {
	t.Run("below threshold: no issue", func(t *testing.T) {
		issues := parseIssues(t, "package p\nfunc f(a, b, c int) {}\n")
		if len(issues) != 0 {
			t.Fatalf("want no issues, got %+v", issues)
		}
	})

	t.Run("at threshold: issue", func(t *testing.T) {
		issues := parseIssues(t, "package p\n"+fourParamFunc())
		if len(issues) != 1 {
			t.Fatalf("want 1 issue, got %+v", issues)
		}
		if issues[0].count != 4 {
			t.Errorf("want count=4, got %d", issues[0].count)
		}
		if issues[0].name != "f" {
			t.Errorf("want name=f, got %q", issues[0].name)
		}
	})

	t.Run("above threshold: issue", func(t *testing.T) {
		issues := parseIssues(t, "package p\nfunc f(a, b, c, d, e int) {}\n")
		if len(issues) != 1 || issues[0].count != 5 {
			t.Fatalf("want one five-parameter issue, got %+v", issues)
		}
	})
}

func TestNolint(t *testing.T) {
	t.Run("//nolint suppresses issue", func(t *testing.T) {
		s := "package p\nfunc f(a, b, c, d int) {} //nolint\n"
		issues := parseIssues(t, s)
		if len(issues) != 0 {
			t.Fatalf("want no issues with //nolint, got %+v", issues)
		}
	})

	t.Run("//nolint:params suppresses issue", func(t *testing.T) {
		s := "package p\nfunc f(a, b, c, d int) {} //nolint:params\n"
		issues := parseIssues(t, s)
		if len(issues) != 0 {
			t.Fatalf("want no issues with //nolint:params, got %+v", issues)
		}
	})

	t.Run("nolint in body does not suppress", func(t *testing.T) {
		s := "package p\nfunc f(a, b, c, d int) {\n\t_ = a //nolint\n}\n"
		issues := parseIssues(t, s)
		if len(issues) != 1 {
			t.Fatalf("want 1 issue (nolint in body ignored), got %+v", issues)
		}
	})
}

func TestReceiverNaming(t *testing.T) {
	s := "package p\ntype T struct{}\nfunc (t T) M(a, b, c, d int) {}\n"
	issues := parseIssues(t, s)
	if len(issues) != 1 {
		t.Fatalf("want 1 issue, got %+v", issues)
	}
	if issues[0].name != "T.M" {
		t.Errorf("want T.M, got %q", issues[0].name)
	}
}
