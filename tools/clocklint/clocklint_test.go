package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func writeGoFile(t *testing.T, name, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runCheckFile(t *testing.T, name, src string) bool {
	t.Helper()
	path := writeGoFile(t, name, src)
	fset := token.NewFileSet()
	hasError := false
	checkFile(path, fset, &hasError)
	return hasError
}

func parseTimeIdents(t *testing.T, src string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return timeImportIdents(f)
}

func TestClockLint(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		wantError bool
	}{
		{
			name: "time.Now() is detected",
			src: `package p
import "time"
func f() { _ = time.Now() }
`,
			wantError: true,
		},
		{
			name: "time.Since() is detected",
			src: `package p
import "time"
func f(t time.Time) { _ = time.Since(t) }
`,
			wantError: true,
		},
		{
			name: "time.NewTimer() is detected",
			src: `package p
import "time"
func f() { _ = time.NewTimer(0) }
`,
			wantError: true,
		},
		{
			name: "time.NewTicker() is detected",
			src: `package p
import "time"
func f() { _ = time.NewTicker(0) }
`,
			wantError: true,
		},
		{
			name: "time.After() is detected",
			src: `package p
import "time"
func f() { _ = time.After(0) }
`,
			wantError: true,
		},
		{
			name: "time.Until() is detected",
			src: `package p
import "time"
func f(t time.Time) { _ = time.Until(t) }
`,
			wantError: true,
		},
		{
			name: "time.Sleep() is NOT detected",
			src: `package p
import "time"
func f() { time.Sleep(0) }
`,
			wantError: false,
		},
		{
			name: "nolint suppresses detection",
			src: `package p
import "time"
func f() { _ = time.Now() } //nolint:clocklint
`,
			wantError: false,
		},
		{
			name: "aliased import is detected",
			src: `package p
import t "time"
func f() { _ = t.Now() }
`,
			wantError: true,
		},
		{
			name: "no time import is not flagged",
			src: `package p
func f() string { return "hello" }
`,
			wantError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runCheckFile(t, "src.go", tc.src)
			if got != tc.wantError {
				t.Errorf("wantError=%v, got=%v", tc.wantError, got)
			}
		})
	}
}

func TestClockLintTests(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		wantError bool
	}{
		{
			name: "clock.System() is detected in unit tests",
			src: `package p
import "github.com/mcpmini/mini/internal/clock"
func TestF() { _ = clock.System() }
`,
			wantError: true,
		},
		{
			name: "clock.NewFake() is allowed in unit tests",
			src: `package p
import "github.com/mcpmini/mini/internal/clock"
func TestF() { _ = clock.NewFake() }
`,
			wantError: false,
		},
		{
			name: "aliased clock.System() is detected in unit tests",
			src: `package p
import c "github.com/mcpmini/mini/internal/clock"
func TestF() { _ = c.System() }
`,
			wantError: true,
		},
		{
			name: "time.After() is allowed in unit tests",
			src: `package p
import "time"
func TestF() { _ = time.After(0) }
`,
			wantError: false,
		},
		{
			name: "nolint suppresses intentional clock.System() in unit tests",
			src: `package p
import "github.com/mcpmini/mini/internal/clock"
func TestF() { _ = clock.System() } //nolint:clocklint
`,
			wantError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runCheckFile(t, "src_test.go", tc.src)
			if got != tc.wantError {
				t.Errorf("wantError=%v, got=%v", tc.wantError, got)
			}
		})
	}
}

func TestTimeImportIdents_standardImport(t *testing.T) {
	idents := parseTimeIdents(t, `package p; import "time"`)
	if !idents["time"] {
		t.Error("expected 'time' to be in idents")
	}
}

func TestTimeImportIdents_aliasedImport(t *testing.T) {
	idents := parseTimeIdents(t, `package p; import tt "time"`)
	if !idents["tt"] {
		t.Error("expected 'tt' to be in idents")
	}
	if idents["time"] {
		t.Error("expected 'time' not to be in idents when aliased")
	}
}

func TestTimeImportIdents_noTimeImport(t *testing.T) {
	idents := parseTimeIdents(t, `package p; import "fmt"`)
	if len(idents) != 0 {
		t.Errorf("expected empty idents, got %v", idents)
	}
}
