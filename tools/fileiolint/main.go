package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	dirs := os.Args[1:]
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	failed := false
	for _, dir := range dirs {
		violations, err := checkTree(dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		for _, v := range violations {
			fmt.Println(v)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func checkTree(root string) ([]string, error) {
	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return skipDir(info.Name())
		}
		found, err := checkFile(path)
		violations = append(violations, found...)
		return err
	})
	return violations, err
}

func skipDir(name string) error {
	switch name {
	case "vendor", ".git", "node_modules", ".agents", ".claude", "testdata":
		return filepath.SkipDir
	}
	return nil
}

func checkFile(path string) ([]string, error) {
	if !strings.HasSuffix(path, "_test.go") {
		return nil, nil
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return checkSource(path, src), nil
}

func checkSource(path string, src []byte) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return []string{fmt.Sprintf("%s:1: cannot parse: %v", path, err)}
	}
	return callDiagnostics(fset, f)
}

func callDiagnostics(fset *token.FileSet, f *ast.File) []string {
	imports := osImports(f)
	calls := fileCalls(f, imports)
	last := make(map[int]token.Pos)
	for _, call := range calls {
		line := fset.Position(call.End()).Line
		last[line] = max(last[line], call.End())
	}
	var violations []string
	for _, call := range calls {
		line := fset.Position(call.End()).Line
		if call.End() == last[line] && exempted(fset, f.Comments, call) {
			continue
		}
		pos := fset.Position(call.Pos())
		name := fileOperation(call.Fun, imports)
		violations = append(violations, fmt.Sprintf("%s:%d: direct os.%s call; use internal/testutil or add //fileiolint:allow <reason> after the call", pos.Filename, pos.Line, name))
	}
	return violations
}

func fileCalls(f *ast.File, imports map[string]bool) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if ok && fileOperation(call.Fun, imports) != "" {
			calls = append(calls, call)
		}
		return true
	})
	return calls
}

func osImports(f *ast.File) map[string]bool {
	names := make(map[string]bool)
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "os" {
			continue
		}
		name := "os"
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = true
	}
	return names
}

func fileOperation(expr ast.Expr, imports map[string]bool) string {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return fileOperation(e.X, imports)
	case *ast.SelectorExpr:
		id, ok := e.X.(*ast.Ident)
		if ok && id.Obj == nil && imports[id.Name] {
			return bannedOperation(e.Sel.Name)
		}
	case *ast.Ident:
		if e.Obj == nil && imports["."] {
			return bannedOperation(e.Name)
		}
	}
	return ""
}

func bannedOperation(name string) string {
	if name == "ReadFile" || name == "WriteFile" {
		return name
	}
	return ""
}

func exempted(fset *token.FileSet, comments []*ast.CommentGroup, call *ast.CallExpr) bool {
	line := fset.Position(call.End()).Line
	for _, group := range comments {
		for _, c := range group.List {
			if c.Pos() >= call.End() && fset.Position(c.Pos()).Line == line && hasReason(c.Text) {
				return true
			}
		}
	}
	return false
}

func hasReason(comment string) bool {
	if !strings.HasPrefix(comment, "//") {
		return false
	}
	_, reason, found := strings.Cut(comment, "//fileiolint:allow ")
	return found && strings.TrimSpace(reason) != ""
}
