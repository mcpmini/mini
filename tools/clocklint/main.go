// clocklint reports direct calls to time.Now, time.Since, time.NewTimer, time.NewTicker,
// time.After, and time.Until in production Go code. Use clock.Clock instead.
// Annotate exemptions with //nolint:clocklint on the offending line.
//
// Usage: clocklint [dir ...]   (default: current directory, recursive)
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

var bannedFuncs = map[string]bool{
	"Now":       true,
	"Since":     true,
	"NewTimer":  true,
	"NewTicker": true,
	"After":     true,
	"Until":     true,
}

func main() {
	dirs := os.Args[1:]
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	hasError := false
	for _, dir := range dirs {
		if err := walkDir(dir, &hasError); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	if hasError {
		os.Exit(1)
	}
}

func walkDir(dir string, hasError *bool) error {
	fset := token.NewFileSet()
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return skipDir(info.Name())
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if isInClockPackage(path) {
			return nil
		}
		checkFile(path, fset, hasError)
		return nil
	})
}

func skipDir(name string) error {
	if name == "vendor" || name == ".git" || name == "node_modules" || name == "clock" {
		return filepath.SkipDir
	}
	return nil
}

func isInClockPackage(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, p := range parts {
		if p == "clock" {
			return true
		}
	}
	return false
}

func checkFile(path string, fset *token.FileSet, hasError *bool) {
	src, err := os.ReadFile(path)
	if err != nil {
		return
	}
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return
	}
	if !importsStdlibTime(f) {
		return
	}
	srcLines := strings.Split(string(src), "\n")
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Name != "time" || !bannedFuncs[sel.Sel.Name] {
			return true
		}
		pos := fset.Position(call.Pos())
		line := pos.Line
		if line > 0 && line <= len(srcLines) && strings.Contains(srcLines[line-1], "//nolint:clocklint") {
			return true
		}
		fmt.Printf("ERROR %s:%d: direct time.%s() call, use clock.Clock instead\n", pos.Filename, line, sel.Sel.Name)
		*hasError = true
		return true
	})
}

func importsStdlibTime(f *ast.File) bool {
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == "time" {
			return true
		}
	}
	return false
}
