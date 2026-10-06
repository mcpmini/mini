package analyzer

import (
	"go/ast"
	"go/types"

	"golang.org/x/exp/typeparams"
	"golang.org/x/tools/go/analysis"
)

// Analyzer requires field names in nonempty literals of structs with at least four fields.
var Analyzer = &analysis.Analyzer{
	Name: "structlint",
	Doc:  "require field names in literals of structs with at least four fields",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if literal, ok := node.(*ast.CompositeLit); ok {
				checkLiteral(pass, literal)
			}
			return true
		})
	}
	return nil, nil
}

func checkLiteral(pass *analysis.Pass, literal *ast.CompositeLit) {
	if len(literal.Elts) == 0 {
		return
	}
	if _, keyed := literal.Elts[0].(*ast.KeyValueExpr); keyed {
		return
	}
	structure := literalStruct(pass.TypesInfo.TypeOf(literal))
	if structure == nil || structure.NumFields() < 4 {
		return
	}
	pass.Report(analysis.Diagnostic{
		Pos:            literal.Pos(),
		End:            literal.End(),
		Message:        "use field names in literals of structs with at least four fields",
		SuggestedFixes: fieldNameFix(structure, literal),
	})
}

func literalStruct(typ types.Type) *types.Struct {
	if typ == nil {
		return nil
	}
	typ = types.Unalias(typ)
	if parameter, ok := typ.(*types.TypeParam); ok {
		return parameterStruct(parameter)
	}
	if pointer, ok := typ.Underlying().(*types.Pointer); ok {
		return literalStruct(pointer.Elem())
	}
	structure, _ := typ.Underlying().(*types.Struct)
	return structure
}

func parameterStruct(parameter *types.TypeParam) *types.Struct {
	terms, err := typeparams.NormalTerms(parameter)
	if err != nil || len(terms) == 0 {
		return nil
	}
	structure := literalStruct(terms[0].Type())
	if structure == nil {
		return nil
	}
	for _, term := range terms[1:] {
		if !sameFieldNames(structure, literalStruct(term.Type())) {
			return nil
		}
	}
	return structure
}

func sameFieldNames(a, b *types.Struct) bool {
	if b == nil || a.NumFields() != b.NumFields() {
		return false
	}
	for i := range a.NumFields() {
		if a.Field(i).Name() != b.Field(i).Name() {
			return false
		}
	}
	return true
}

func fieldNameFix(structure *types.Struct, literal *ast.CompositeLit) []analysis.SuggestedFix {
	if structure.NumFields() != len(literal.Elts) {
		return nil
	}
	var edits []analysis.TextEdit
	for i, value := range literal.Elts {
		name := structure.Field(i).Name()
		if name == "_" {
			return nil
		}
		edits = append(edits, analysis.TextEdit{Pos: value.Pos(), NewText: []byte(name + ": ")})
	}
	return []analysis.SuggestedFix{{Message: "Insert field names", TextEdits: edits}}
}
