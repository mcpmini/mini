package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

type sourceFile struct {
	path      string
	file      *ast.File
	fset      *token.FileSet
	lines     []string
	skippable []bool
}

// readSource returns false for a file that can't be read or parsed; the compiler reports those.
func readSource(path string, fset *token.FileSet) (*sourceFile, bool) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return parseSource(path, src, fset)
}

func parseSource(path string, src []byte, fset *token.FileSet) (*sourceFile, bool) {
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, false
	}
	lines := strings.Split(string(src), "\n")
	return &sourceFile{path: path, file: f, fset: fset, lines: lines, skippable: skippableLines(lines)}, true
}

func (s *sourceFile) funcDecls() []*ast.FuncDecl {
	var decls []*ast.FuncDecl
	for _, decl := range s.file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Body != nil {
			decls = append(decls, fd)
		}
	}
	return decls
}

func (s *sourceFile) line(pos token.Pos) int {
	return s.fset.Position(pos).Line
}

func (s *sourceFile) codeLines(from, to int, excluded map[int]bool) int {
	count := 0
	for line := from; line <= to && line < len(s.skippable); line++ {
		if !s.skippable[line] && !excluded[line] {
			count++
		}
	}
	return count
}

// skippableLines marks, by 1-based line number, the lines that don't represent real logic:
// comment-only lines and bare closing braces. A line that mixes code with a trailing comment
// still counts as code.
func skippableLines(lines []string) []bool {
	skippable := make([]bool, len(lines)+1)
	inBlock := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		var isComment bool
		isComment, inBlock = classifyCommentLine(trimmed, inBlock)
		skippable[i+1] = isComment || trimmed == "}"
	}
	return skippable
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
