package main

import (
	"fmt"
	"go/token"
)

// testFunc is a function in a test file, judged by its longest leaf scope.
type testFunc struct {
	name    string
	path    string
	longest scope
}

func (f testFunc) describe() string {
	return fmt.Sprintf("%s:%d: %s/%s", f.path, f.longest.line, f.name, f.longest.name)
}

// measureTests keys each function by package directory and name. The internal and external test
// packages share a directory, so a name can repeat there; the larger measure wins.
func measureTests(paths []string, fset *token.FileSet) map[string]testFunc {
	funcs := map[string]testFunc{}
	for _, path := range paths {
		s, ok := readSource(path, fset)
		if !ok {
			continue
		}
		for _, f := range s.testFuncs() {
			key := pkgDir(path) + ":" + f.name
			if prev, seen := funcs[key]; !seen || f.longest.lines > prev.longest.lines {
				funcs[key] = f
			}
		}
	}
	return funcs
}

func (s *sourceFile) testFuncs() []testFunc {
	var funcs []testFunc
	for _, fd := range s.funcDecls() {
		f := testFunc{name: funcName(fd), path: s.path}
		for _, sc := range s.leafScopes(fd.Body, "body", s.line(fd.Pos())) {
			if sc.lines > f.longest.lines || f.longest.name == "" {
				f.longest = sc
			}
		}
		funcs = append(funcs, f)
	}
	return funcs
}
