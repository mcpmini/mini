// params checks that no function or method has >= 4 parameters.
// Functions with that many parameters should use a params struct instead.
// Exit code 1 when any violations are found.
//
// Usage: params [dir ...]   (default: current directory, recursive)
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

// maxParams is temporarily raised to 8 to allow pre-existing violations to not block the build.
// P2 debt: convert all functions with 4+ parameters to use params structs and ratchet this back to 4.
const maxParams = 8

type issue struct {
	path  string
	line  int
	name  string
	count int
}

func main() {
	dirs := os.Args[1:]
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	issues := collect(dirs)
	for _, iss := range issues {
		fmt.Printf("ERROR %s:%d: %s has %d parameters (>= %d): use a params struct\n",
			iss.path, iss.line, iss.name, iss.count, maxParams)
	}
	if len(issues) > 0 {
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
	n := countFields(fd.Type.Params)
	if n < maxParams {
		return issue{}, false
	}
	pos := fset.Position(fd.Pos())
	return issue{
		path:  pos.Filename,
		line:  pos.Line,
		name:  funcName(fd),
		count: n,
	}, true
}

func countFields(fl *ast.FieldList) int {
	if fl == nil {
		return 0
	}
	total := 0
	for _, f := range fl.List {
		if len(f.Names) == 0 {
			total++
		} else {
			total += len(f.Names)
		}
	}
	return total
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
