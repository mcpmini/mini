package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func TestDirectFileCallsRequireReasonedExemptions(t *testing.T) {
	cases := []struct {
		name, imports, body string
		want                string
	}{
		{"read", `import "os"`, `os.ReadFile("fixture")`, "direct os.ReadFile call"},
		{"write alias", `import fs "os"`, `fs.WriteFile("fixture", nil, 0600)`, "direct os.WriteFile call"},
		{"dot import", `import . "os"`, `ReadFile("fixture")`, "direct os.ReadFile call"},
		{"parenthesized", `import "os"`, `(os.ReadFile)("fixture")`, "direct os.ReadFile call"},
		{"unrelated package", `import "example/fs"`, `fs.ReadFile("fixture")`, ""},
		{"shadowed alias", `import fs "os"`, `fs := struct{ReadFile func(string)}{}; fs.ReadFile("fixture")`, ""},
		{"shadowed dot name", `import . "os"`, `ReadFile := func(string) {}; ReadFile("fixture")`, ""},
		{"other os operation", `import "os"`, `os.Stat("fixture")`, ""},
		{"reason", `import "os"`, "os.ReadFile(\"fixture\") //fileiolint:allow poll for output", ""},
		{"no reason", `import "os"`, "os.ReadFile(\"fixture\") //fileiolint:allow ", "direct os.ReadFile call"},
		{
			"whitespace reason",
			`import "os"`,
			"os.ReadFile(\"fixture\") //fileiolint:allow \t ",
			"direct os.ReadFile call",
		},
		{
			"preceding comment",
			`import "os"`,
			"//fileiolint:allow poll for output\nos.ReadFile(\"fixture\")",
			"direct os.ReadFile call",
		},
		{
			"string marker",
			`import "os"`,
			`os.ReadFile("//fileiolint:allow poll for output")`,
			"direct os.ReadFile call",
		},
		{
			"block comment",
			`import "os"`,
			`os.ReadFile("fixture") /* //fileiolint:allow poll for output */`,
			"direct os.ReadFile call",
		},
		{
			"another linter",
			`import "os"`,
			"os.WriteFile(\"fixture\", nil, 0600) //nolint:errcheck //fileiolint:allow executable script",
			"",
		},
		{"multiline", `import "os"`, "os.ReadFile(\n\"fixture\",\n) //fileiolint:allow poll for output", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package fixture\n" + tc.imports + "\nfunc f() {\n" + tc.body + "\n}\n"
			got := checkSource("fixture_test.go", []byte(src))
			assertViolation(t, got, tc.want)
		})
	}
}

func assertViolation(t *testing.T, got []string, want string) {
	t.Helper()
	if want == "" {
		if len(got) != 0 {
			t.Fatalf("unexpected diagnostics: %v", got)
		}
		return
	}
	if len(got) != 1 || !strings.Contains(got[0], want) {
		t.Fatalf("diagnostics = %v; want one containing %q", got, want)
	}
}

func TestDiagnosticsIdentifyEachCallAndItsLine(t *testing.T) {
	src := "package fixture\nimport \"os\"\nfunc f() {\nos.ReadFile(\"a\")\nos.WriteFile(\"b\", nil, 0600)\n}\n"
	got := checkSource("fixture_test.go", []byte(src))
	if len(got) != 2 {
		t.Fatalf("diagnostics = %v; want two", got)
	}
	for i, want := range []string{"fixture_test.go:4: direct os.ReadFile", "fixture_test.go:5: direct os.WriteFile"} {
		if !strings.HasPrefix(got[i], want) {
			t.Errorf("diagnostic = %q; want prefix %q", got[i], want)
		}
	}
}

func TestExemptionDoesNotHideTheNextCall(t *testing.T) {
	src := "package fixture\nimport \"os\"\nfunc f() {\nos.ReadFile(\"a\") //fileiolint:allow poll\nos.ReadFile(\"b\")\n}\n"
	got := checkSource("fixture_test.go", []byte(src))
	assertViolation(t, got, "fixture_test.go:5: direct os.ReadFile")
}

func TestCheckTreeIncludesIntegrationTestsAndSkipsFixtureDirectories(t *testing.T) {
	root := t.TempDir()
	src := "package fixture\nimport \"os\"\nfunc f() { os.ReadFile(\"a\") }\n"
	for _, name := range []string{"unit_test.go", "flow_integration_test.go", "production.go", "testdata/fixture_test.go", "vendor/fixture_test.go", ".git/fixture_test.go", ".agents/fixture_test.go", ".claude/fixture_test.go", "node_modules/fixture_test.go"} {
		testutil.WriteFile(t, filepath.Join(root, name), src)
	}
	got, err := checkTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("diagnostics = %v; want unit and integration violations", got)
	}
	for i, name := range []string{"flow_integration_test.go", "unit_test.go"} {
		if !strings.Contains(got[i], name+":3:") {
			t.Errorf("diagnostic = %q; want %s", got[i], name)
		}
	}
}

func TestCheckTreeReportsMissingRoots(t *testing.T) {
	if _, err := checkTree(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing root must return an error")
	}
}

func TestCheckSourceReportsInvalidSyntax(t *testing.T) {
	assertViolation(t, checkSource("broken_test.go", []byte("package")), "broken_test.go:1: cannot parse")
}

func TestExemptionAppliesOnlyToTheLastFileCallOnItsLine(t *testing.T) {
	src := "package fixture\nimport \"os\"\nfunc f() {\nos.ReadFile(\"a\"); os.WriteFile(\"b\", nil, 0600) //fileiolint:allow executable script\n}\n"
	assertViolation(t, checkSource("fixture_test.go", []byte(src)), "direct os.ReadFile call")
}
