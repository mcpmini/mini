package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

type issue struct {
	path    string
	line    int
	name    string
	lines   int
	isError bool
}

func (iss issue) String() string {
	level, threshold := "WARNING", warnAt
	if iss.isError {
		level, threshold = "ERROR", errorAt
	}
	where := fmt.Sprintf("%s:%d", iss.path, iss.line)
	return fmt.Sprintf("%s %s: %s is %d code lines (>= %d)", level, where, iss.name, iss.lines, threshold)
}

func checkProduction(paths []string, fset *token.FileSet) []issue {
	var issues []issue
	for _, path := range paths {
		if s, ok := readSource(path, fset); ok {
			issues = append(issues, s.funcIssues()...)
		}
	}
	return issues
}

func (s *sourceFile) funcIssues() []issue {
	var issues []issue
	for _, fd := range s.funcDecls() {
		if iss, found := s.checkFunc(fd); found {
			issues = append(issues, iss)
		}
	}
	return issues
}

func (s *sourceFile) checkFunc(fd *ast.FuncDecl) (issue, bool) {
	start, end := s.line(fd.Pos()), s.line(fd.End())
	lines := s.codeLines(start+1, end-1, nil)
	if lines < warnAt {
		return issue{}, false
	}
	if lines < errorAt && s.isNolinted(start) {
		return issue{}, false
	}
	return issue{path: s.path, line: start, name: funcName(fd), lines: lines, isError: lines >= errorAt}, true
}

func (s *sourceFile) isNolinted(line int) bool {
	if line < 1 || line > len(s.lines) {
		return false
	}
	return strings.Contains(s.lines[line-1], "//nolint:funclen")
}
