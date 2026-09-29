// testkind enforces the integration test convention: integration test files are
// named *_integration_test.go, carry the "integration" build constraint, and
// declare only TestIntegration... tests; no other test file declares one.
//
// Usage: testkind [dir ...]   (default: current directory, recursive)
package main

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

const (
	integrationFileSuffix = "_integration_test.go"
	integrationTestPrefix = "TestIntegration"
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
	case "vendor", ".git", ".claude", ".agents", "testdata":
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
	tagged := hasIntegrationConstraint(f)
	named := strings.HasSuffix(path, integrationFileSuffix)
	if msg := fileMismatch(tagged, named); msg != "" {
		return []string{fmt.Sprintf("%s:1: %s", path, msg)}
	}
	return checkTestNames(fset, f, tagged)
}

func fileMismatch(tagged, named bool) string {
	switch {
	case tagged && !named:
		return "file has the integration build constraint but is not named *" + integrationFileSuffix
	case named && !tagged:
		return "file is named *" + integrationFileSuffix + " but lacks the integration build constraint"
	}
	return ""
}

func checkTestNames(fset *token.FileSet, f *ast.File, integrationFile bool) []string {
	var violations []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !isTestFunc(fn) {
			continue
		}
		if msg := nameMismatch(fn.Name.Name, integrationFile); msg != "" {
			pos := fset.Position(fn.Pos())
			violations = append(violations, fmt.Sprintf("%s:%d: %s", pos.Filename, pos.Line, msg))
		}
	}
	return violations
}

func nameMismatch(name string, integrationFile bool) string {
	prefixed := strings.HasPrefix(name, integrationTestPrefix)
	switch {
	case integrationFile && !prefixed:
		return name + " in an integration file must be named " + integrationTestPrefix + "..."
	case !integrationFile && prefixed:
		return name + " uses the " + integrationTestPrefix + " prefix but the file is not an integration test file"
	}
	return ""
}

func isTestFunc(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
		return false
	}
	params := fn.Type.Params.List
	if len(params) != 1 {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}

func hasIntegrationConstraint(f *ast.File) bool {
	for _, group := range f.Comments {
		if group.Pos() >= f.Package {
			break
		}
		for _, c := range group.List {
			if isIntegrationBuildLine(c.Text) {
				return true
			}
		}
	}
	return false
}

func isIntegrationBuildLine(text string) bool {
	if !constraint.IsGoBuild(text) {
		return false
	}
	expr, err := constraint.Parse(text)
	return err == nil && mentionsPositively(expr, false)
}

func mentionsPositively(expr constraint.Expr, negated bool) bool {
	switch e := expr.(type) {
	case *constraint.TagExpr:
		return e.Tag == "integration" && !negated
	case *constraint.NotExpr:
		return mentionsPositively(e.X, !negated)
	case *constraint.AndExpr:
		return mentionsPositively(e.X, negated) || mentionsPositively(e.Y, negated)
	case *constraint.OrExpr:
		return mentionsPositively(e.X, negated) || mentionsPositively(e.Y, negated)
	}
	return false
}
