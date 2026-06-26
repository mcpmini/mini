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

// Sleep is intentionally excluded: it's a real delay, not a clock read.
// Use clock.NewTimer + select for fakeable sleeps in library code.
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
	switch name {
	case "vendor", ".git", "node_modules", "clock", ".agents", ".claude", "cmd", "evals", "test":
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
	timeIdents := timeImportIdents(f)
	if len(timeIdents) == 0 {
		return
	}
	srcLines := strings.Split(string(src), "\n")
	ast.Inspect(f, func(n ast.Node) bool {
		return inspectTimeCalls(n, fset, srcLines, timeIdents, hasError)
	})
}

func inspectTimeCalls(n ast.Node, fset *token.FileSet, srcLines []string, timeIdents map[string]bool, hasError *bool) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return true
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return true
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || !timeIdents[id.Name] || !bannedFuncs[sel.Sel.Name] {
		return true
	}
	pos := fset.Position(call.Pos())
	if isNolinted(srcLines, pos.Line) {
		return true
	}
	fmt.Printf("ERROR %s:%d: direct time.%s() call, use clock.Clock instead\n", pos.Filename, pos.Line, sel.Sel.Name)
	*hasError = true
	return true
}

func isNolinted(srcLines []string, line int) bool {
	return line > 0 && line <= len(srcLines) && strings.Contains(srcLines[line-1], "//nolint:clocklint")
}

func timeImportIdents(f *ast.File) map[string]bool {
	out := make(map[string]bool)
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != "time" {
			continue
		}
		if imp.Name != nil {
			out[imp.Name.Name] = true
		} else {
			out["time"] = true
		}
	}
	return out
}
