package main

import (
	"go/ast"
	"go/parser"
	"go/scanner"
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

func readSource(path string, fset *token.FileSet) (*sourceFile, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseSource(path, src, fset)
}

func parseSource(path string, src []byte, fset *token.FileSet) (*sourceFile, error) {
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(src), "\n")
	return &sourceFile{path: path, file: f, fset: fset, lines: lines, skippable: skippableLines(src, lines)}, nil
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
// still counts as code, and so does a blank one.
func skippableLines(src []byte, lines []string) []bool {
	code := tokenLines(src, len(lines))
	skippable := make([]bool, len(lines)+1)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		skippable[i+1] = trimmed == "}" || (trimmed != "" && !code[i+1])
	}
	return skippable
}

// Tokenizing rather than matching text keeps a "/*" inside a raw string from reading as a comment.
func tokenLines(src []byte, lineCount int) []bool {
	code := make([]bool, lineCount+2)
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, scanner.ScanComments)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return code
		}
		if tok == token.COMMENT {
			continue
		}
		for l := file.Line(pos); l <= file.Line(pos+token.Pos(max(len(lit), 1)-1)); l++ {
			code[l] = true
		}
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
