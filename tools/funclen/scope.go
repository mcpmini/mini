package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
)

type scope struct {
	name  string
	line  int
	lines int
}

type subtest struct {
	call *ast.CallExpr
	fn   *ast.FuncLit
}

type scopeChildren struct {
	subtests  []subtest
	tables    []*ast.CompositeLit
	callbacks []*ast.FuncLit
}

func (s *sourceFile) leafScopes(body *ast.BlockStmt, name string, line int) []scope {
	children := directChildren(body)
	excluded := map[int]bool{}
	for _, table := range children.tables {
		s.markLines(excluded, s.line(table.Pos()), s.line(table.End()))
	}
	for _, child := range children.subtests {
		s.markLines(excluded, s.line(child.fn.Body.Lbrace)+1, s.line(child.fn.Body.Rbrace)-1)
	}
	own := s.codeLines(s.line(body.Lbrace)+1, s.line(body.Rbrace)-1, excluded)
	scopes := []scope{{name: name, line: line, lines: own}}
	for _, child := range children.subtests {
		childName := nestedName(name, s.subtestName(child.call))
		scopes = append(scopes, s.leafScopes(child.fn.Body, childName, s.line(child.call.Pos()))...)
	}
	for _, fn := range children.callbacks {
		fnLine := s.line(fn.Pos())
		scopes = append(scopes, s.leafScopes(fn.Body, nestedName(name, fmt.Sprintf("func@%d", fnLine)), fnLine)...)
	}
	return scopes
}

func (s *sourceFile) markLines(lines map[int]bool, from, to int) {
	for l := from; l <= to; l++ {
		lines[l] = true
	}
}

func nestedName(parent, child string) string {
	if parent == "body" {
		return child
	}
	return parent + "/" + child
}

// directChildren finds the subtests and tables directly inside body in one walk, stopping at
// each, so whatever is nested in them is measured only by their own scope.
func directChildren(body *ast.BlockStmt) scopeChildren {
	var found scopeChildren
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if fn, ok := subtestFunc(n); ok {
				found.subtests = append(found.subtests, subtest{call: n, fn: fn})
				return false
			}
		case *ast.CompositeLit:
			if isTable(n) {
				found.tables = append(found.tables, n)
				found.callbacks = append(found.callbacks, tableCallbacks(n)...)
				return false
			}
		}
		return true
	})
	return found
}

func subtestFunc(call *ast.CallExpr) (*ast.FuncLit, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Run" || len(call.Args) != 2 {
		return nil, false
	}
	fn, ok := call.Args[1].(*ast.FuncLit)
	return fn, ok && takesTestingArg(fn)
}

func takesTestingArg(fn *ast.FuncLit) bool {
	params := fn.Type.Params.List
	if len(params) != 1 {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing" && (sel.Sel.Name == "T" || sel.Sel.Name == "B")
}

func (s *sourceFile) subtestName(call *ast.CallExpr) string {
	if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if name, err := strconv.Unquote(lit.Value); err == nil {
			return name
		}
	}
	return fmt.Sprintf("t.Run@%d", s.line(call.Pos()))
}
