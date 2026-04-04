// funclen checks that no function or method exceeds the project line limits.
// Warning >= 20 lines, error >= 30 lines. Exit code 1 when any errors are found.
//
// Usage: funclen [dir ...]   (default: current directory, recursive)
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

const (
	warnAt  = 15
	errorAt = 25
)

type issue struct {
	path    string
	line    int
	name    string
	lines   int
	isError bool
}

func main() {
	dirs := os.Args[1:]
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	issues := collect(dirs)
	hasError := false
	for _, iss := range issues {
		level, threshold := "WARNING", warnAt
		if iss.isError {
			level, threshold, hasError = "ERROR", errorAt, true
		}
		fmt.Printf("%s %s:%d: %s is %d lines (>= %d)\n", level, iss.path, iss.line, iss.name, iss.lines, threshold)
	}
	if hasError {
		os.Exit(1)
	}
}

func collect(dirs []string) []issue {
	fset := token.NewFileSet()
	var issues []issue
	for _, dir := range dirs {
		if err := walkDir(dir, fset, &issues); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	return issues
}

func walkDir(dir string, fset *token.FileSet, issues *[]issue) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && isSkipped(info.Name()) {
			return filepath.SkipDir
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		*issues = append(*issues, checkFile(path, fset)...)
		return nil
	})
}

func isSkipped(name string) bool {
	return name == "vendor" || name == ".git" || name == "node_modules"
}

func checkFile(path string, fset *token.FileSet) []issue {
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil
	}
	var issues []issue
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			return true
		}
		if iss, found := checkFunc(fd, fset); found {
			issues = append(issues, iss)
		}
		return true
	})
	return issues
}

func checkFunc(fd *ast.FuncDecl, fset *token.FileSet) (issue, bool) {
	lines := fset.Position(fd.End()).Line - fset.Position(fd.Pos()).Line + 1
	if lines < warnAt {
		return issue{}, false
	}
	pos := fset.Position(fd.Pos())
	return issue{
		path:    pos.Filename,
		line:    pos.Line,
		name:    funcName(fd),
		lines:   lines,
		isError: lines >= errorAt,
	}, true
}

func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	if recv := recvTypeName(fd.Recv.List[0]); recv != "" {
		return recv + "." + fd.Name.Name
	}
	return fd.Name.Name
}

func recvTypeName(f *ast.Field) string {
	switch t := f.Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}
