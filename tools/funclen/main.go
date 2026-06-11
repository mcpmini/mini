// funclen checks that no function or method exceeds the project code-line limits.
// Warning >= 15 lines, error >= 25 lines. Comment-only lines inside the function
// body don't count, so a well-documented invariant doesn't inflate the length.
// Functions annotated with //nolint on their declaration line are skipped.
// Exit code 1 when any errors are found.
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
		if iss.isError {
			hasError = true
		}
		printIssue(iss)
	}
	if hasError {
		os.Exit(1)
	}
}

func printIssue(iss issue) {
	level, threshold := "WARNING", warnAt
	if iss.isError {
		level, threshold = "ERROR", errorAt
	}
	fmt.Printf("%s %s:%d: %s is %d code lines (>= %d)\n", level, iss.path, iss.line, iss.name, iss.lines, threshold)
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
		if !strings.HasSuffix(path, ".go") || isTestFile(path) {
			return nil
		}
		*issues = append(*issues, checkFile(path, fset)...)
		return nil
	})
}

func isSkipped(name string) bool {
	return name == "vendor" || name == ".git" || name == "node_modules"
}

func isTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}

func checkFile(path string, fset *token.FileSet) []issue {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil
	}
	return collectFuncIssues(f, fset, strings.Split(string(src), "\n"))
}

func collectFuncIssues(f *ast.File, fset *token.FileSet, srcLines []string) []issue {
	var issues []issue
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			return true
		}
		if iss, found := checkFunc(fd, fset, srcLines); found {
			issues = append(issues, iss)
		}
		return true
	})
	return issues
}

func checkFunc(fd *ast.FuncDecl, fset *token.FileSet, srcLines []string) (issue, bool) {
	start := fset.Position(fd.Pos()).Line
	if isNolinted(srcLines, start) {
		return issue{}, false
	}
	end := fset.Position(fd.End()).Line
	lines := (end - start - 1) - commentOnlyLines(srcLines, start+1, end-1)
	if lines < warnAt {
		return issue{}, false
	}
	return newIssue(fd, fset, lines), true
}

func isNolinted(srcLines []string, line int) bool {
	if line < 1 || line > len(srcLines) {
		return false
	}
	return strings.Contains(srcLines[line-1], "//nolint")
}

func newIssue(fd *ast.FuncDecl, fset *token.FileSet, lines int) issue {
	pos := fset.Position(fd.Pos())
	return issue{
		path:    pos.Filename,
		line:    pos.Line,
		name:    funcName(fd),
		lines:   lines,
		isError: lines >= errorAt,
	}
}

// commentOnlyLines counts lines in [start, end] (1-indexed, inclusive) that
// consist solely of a // or /* */ comment, ignoring surrounding whitespace.
// A line that mixes code with a trailing comment still counts as code.
func commentOnlyLines(srcLines []string, start, end int) int {
	count := 0
	inBlock := false
	for i := start; i <= end && i <= len(srcLines); i++ {
		var isComment bool
		isComment, inBlock = classifyCommentLine(strings.TrimSpace(srcLines[i-1]), inBlock)
		if isComment {
			count++
		}
	}
	return count
}

// classifyCommentLine reports whether line is comment-only, and whether a
// /* */ block comment remains open for the next line.
func classifyCommentLine(line string, inBlock bool) (isComment, stillInBlock bool) {
	switch {
	case inBlock:
		return true, !strings.Contains(line, "*/")
	case strings.HasPrefix(line, "//"):
		return true, false
	case strings.HasPrefix(line, "/*"):
		return true, !strings.Contains(line, "*/")
	default:
		return false, false
	}
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
