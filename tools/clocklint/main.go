// clocklint reports direct calls to time.Now, time.Since, time.NewTimer, time.NewTicker,
// time.After, and time.Until in production Go code. It also reports clock.System()
// in unit tests. Use clock.Clock or clock.NewFake instead.
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
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		checkFile(path, fset, hasError)
		return nil
	})
}

func skipDir(name string) error {
	switch name {
	case "vendor", ".git", "node_modules", "clock", ".agents", ".claude", "evals", "test":
		return filepath.SkipDir
	}
	return nil
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
	ctx := newCallContext(path, f)
	if len(ctx.timeIdents) == 0 && len(ctx.clockIdents) == 0 {
		return
	}
	inspectFileCalls(f, fset, strings.Split(string(src), "\n"), ctx, hasError)
}

func newCallContext(path string, f *ast.File) callContext {
	timeIdents := timeImportIdents(f)
	clockIdents := clockImportIdents(f)
	return callContext{timeIdents: timeIdents, clockIdents: clockIdents, isTest: strings.HasSuffix(path, "_test.go")}
}

func inspectFileCalls(f *ast.File, fset *token.FileSet, srcLines []string, ctx callContext, hasError *bool) {
	ast.Inspect(f, func(n ast.Node) bool {
		return inspectCalls(n, fset, srcLines, ctx, hasError)
	})
}

type callContext struct {
	timeIdents  map[string]bool
	clockIdents map[string]bool
	isTest      bool
}

func inspectCalls(n ast.Node, fset *token.FileSet, srcLines []string, ctx callContext, hasError *bool) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return true
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return true
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return true
	}
	pos := fset.Position(call.Pos())
	if isNolinted(srcLines, pos.Line) {
		return true
	}
	reportCall(pos, id.Name, sel.Sel.Name, ctx, hasError)
	return true
}

func reportCall(pos token.Position, ident, name string, ctx callContext, hasError *bool) {
	if shouldReportTimeCall(ident, name, ctx) {
		fmt.Printf("ERROR %s:%d: direct time.%s() call, use clock.Clock instead\n", pos.Filename, pos.Line, name)
		*hasError = true
	}
	if shouldReportClockCall(ident, name, ctx) {
		fmt.Printf("ERROR %s:%d: clock.System() in unit test, use clock.NewFake() or a test fake instead\n", pos.Filename, pos.Line)
		*hasError = true
	}
}

func shouldReportTimeCall(ident, name string, ctx callContext) bool {
	return !ctx.isTest && ctx.timeIdents[ident] && bannedFuncs[name]
}

func shouldReportClockCall(ident, name string, ctx callContext) bool {
	return ctx.isTest && ctx.clockIdents[ident] && name == "System"
}

func clockImportIdents(f *ast.File) map[string]bool {
	out := make(map[string]bool)
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != "github.com/mcpmini/mini/internal/clock" {
			continue
		}
		if imp.Name != nil {
			out[imp.Name.Name] = true
		} else {
			out["clock"] = true
		}
	}
	return out
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
