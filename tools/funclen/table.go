package main

import (
	"go/ast"
	"go/token"
)

// tableLines marks the lines of every table literal in body. A table is a slice or map literal
// holding two or more composite literals, however the test consumes it; a single struct literal
// is setup and still counts. Nested map[string]any fixtures match too, which the AST alone
// can't tell apart from tables.
func (s *sourceFile) tableLines(body *ast.BlockStmt) map[int]bool {
	lines := map[int]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isTable(lit) {
			return true
		}
		for l := s.line(lit.Pos()); l <= s.line(lit.End()); l++ {
			lines[l] = true
		}
		return false
	})
	return lines
}

func isTable(lit *ast.CompositeLit) bool {
	switch lit.Type.(type) {
	case *ast.ArrayType, *ast.MapType:
	default:
		return false
	}
	rows := 0
	for _, elt := range lit.Elts {
		if isCompositeRow(elt) {
			rows++
		}
	}
	return rows >= 2
}

func isCompositeRow(elt ast.Expr) bool {
	if kv, ok := elt.(*ast.KeyValueExpr); ok {
		elt = kv.Value
	}
	if addr, ok := elt.(*ast.UnaryExpr); ok && addr.Op == token.AND {
		elt = addr.X
	}
	_, ok := elt.(*ast.CompositeLit)
	return ok
}
