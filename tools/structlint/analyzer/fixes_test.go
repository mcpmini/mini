package analyzer

import (
	"go/format"
	"go/token"
	"slices"
	"testing"

	"golang.org/x/tools/go/analysis"
)

func TestFixPreservesCommentsAndExpressionOrder(t *testing.T) {
	source := `package fixture

type Quad struct{ A, B, C, D int }
func call(n int) int { return n }
var _ = Quad{
	call(1), //nolint:fixture // first expression
	/* second expression */ call(2),
	call(3), call(4),
}`
	want := `package fixture

type Quad struct{ A, B, C, D int }
func call(n int) int { return n }
var _ = Quad{
	A: call(1), //nolint:fixture // first expression
	/* second expression */ B: call(2),
	C: call(3), D: call(4),
}`
	fset, diagnostics := analyzeSource(t, source)
	fixed := applyFixes(t, source, fset, diagnostics)
	if fixed != formatted(t, want) {
		t.Fatalf("fixed source:\n%s\nwant:\n%s", fixed, formatted(t, want))
	}
	_, remaining := analyzeSource(t, fixed)
	if len(remaining) != 0 {
		t.Fatalf("fixed source still needs changes: %v", remaining)
	}
}

func TestNestedFixesCompose(t *testing.T) {
	source := `package fixture

type Quad struct{ A, B, C, D int }
type Outer struct{ First, Second, Third, Fourth Quad }
var _ = Outer{Quad{1, 2, 3, 4}, Quad{}, Quad{}, Quad{5, 6, 7, 8}}`
	want := `package fixture

type Quad struct{ A, B, C, D int }
type Outer struct{ First, Second, Third, Fourth Quad }
var _ = Outer{First: Quad{A: 1, B: 2, C: 3, D: 4}, Second: Quad{}, Third: Quad{}, Fourth: Quad{A: 5, B: 6, C: 7, D: 8}}`
	fset, diagnostics := analyzeSource(t, source)
	if len(diagnostics) != 3 {
		t.Fatalf("diagnostics = %v, want three nested fixes", diagnostics)
	}
	fixed := applyFixes(t, source, fset, diagnostics)
	if fixed != formatted(t, want) {
		t.Fatalf("nested fixes:\n%s\nwant:\n%s", fixed, formatted(t, want))
	}
	_, remaining := analyzeSource(t, fixed)
	if len(remaining) != 0 {
		t.Fatalf("fixed nested source still needs changes: %v", remaining)
	}
}

func TestImportedFieldsUseTheirDeclaredNames(t *testing.T) {
	source := "package fixture\nimport pos \"go/token\"\nvar _ = pos.Position{\"fixture\", 0, 1, 2}"
	want := "package fixture\nimport pos \"go/token\"\nvar _ = pos.Position{Filename: \"fixture\", Offset: 0, Line: 1, Column: 2}"
	fset, diagnostics := analyzeSource(t, source)
	fixed := applyFixes(t, source, fset, diagnostics)
	if fixed != formatted(t, want) {
		t.Fatalf("imported fields:\n%s\nwant:\n%s", fixed, formatted(t, want))
	}
	analyzeSource(t, fixed)
}

func applyFixes(t *testing.T, source string, fset *token.FileSet, diagnostics []analysis.Diagnostic) string {
	t.Helper()
	var edits []analysis.TextEdit
	for _, diagnostic := range diagnostics {
		if len(diagnostic.SuggestedFixes) != 1 {
			t.Fatalf("expected one fix: %v", diagnostic)
		}
		edits = append(edits, diagnostic.SuggestedFixes[0].TextEdits...)
	}
	slices.SortFunc(edits, func(a, b analysis.TextEdit) int { return int(b.Pos - a.Pos) })
	for _, edit := range edits {
		pos := fset.Position(edit.Pos).Offset
		source = source[:pos] + string(edit.NewText) + source[pos:]
	}
	return formatted(t, source)
}

func formatted(t *testing.T, source string) string {
	t.Helper()
	result, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return string(result)
}
