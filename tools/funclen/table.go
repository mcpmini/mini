package main

import (
	"go/ast"
	"go/token"
)

// A table is a slice or map literal holding two or more composite literals, however the test
// consumes it; a single struct literal is setup and still counts. Nested map[string]any fixtures
// match too, which the AST alone can't tell apart from tables.
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

func tableCallbacks(table *ast.CompositeLit) []*ast.FuncLit {
	var found []*ast.FuncLit
	ast.Inspect(table, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncLit)
		if ok {
			found = append(found, fn)
		}
		return !ok
	})
	return found
}
