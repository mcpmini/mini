package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
)

// scope is a block measured on its own: a test function's body, or the body of a t.Run inside
// it, with its own subtests' lines removed.
type scope struct {
	name  string // "body", or the subtest path such as "valid/empty" or "t.Run@42"
	line  int
	lines int
}

type subtest struct {
	call *ast.CallExpr
	fn   *ast.FuncLit
}

func (s *sourceFile) leafScopes(body *ast.BlockStmt, name string, line int) []scope {
	children := subtests(body)
	excluded := s.tableLines(body)
	for _, child := range children {
		for l := s.line(child.fn.Body.Lbrace) + 1; l < s.line(child.fn.Body.Rbrace); l++ {
			excluded[l] = true
		}
	}
	own := s.codeLines(s.line(body.Lbrace)+1, s.line(body.Rbrace)-1, excluded)
	scopes := []scope{{name: name, line: line, lines: own}}
	for _, child := range children {
		childName := s.subtestName(child.call)
		if name != "body" {
			childName = name + "/" + childName
		}
		scopes = append(scopes, s.leafScopes(child.fn.Body, childName, s.line(child.call.Pos()))...)
	}
	return scopes
}

// subtests returns the t.Run calls directly inside body; the ones nested in them belong to
// their own scope.
func subtests(body *ast.BlockStmt) []subtest {
	var found []subtest
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if fn, ok := subtestFunc(call); ok {
			found = append(found, subtest{call: call, fn: fn})
			return false
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

// subtestName is the literal name, or t.Run@line when the name is computed (tt.name), which
// stays readable without evaluating it.
func (s *sourceFile) subtestName(call *ast.CallExpr) string {
	if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if name, err := strconv.Unquote(lit.Value); err == nil {
			return name
		}
	}
	return fmt.Sprintf("t.Run@%d", s.line(call.Pos()))
}
