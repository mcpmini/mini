package main

import (
	"fmt"
	"go/token"
)

type testFunc struct {
	name    string
	path    string
	longest scope
}

func (f testFunc) describe() string {
	return fmt.Sprintf("%s:%d: %s/%s", f.path, f.longest.line, f.name, f.longest.name)
}

// measureTests keys each function by package directory and name. The internal and external test
// packages share a directory, so a name can repeat there; the larger measure wins. A file that
// can't be parsed is an error rather than a skip, so a prune never drops the allowances it holds.
func measureTests(paths []string, fset *token.FileSet) (map[string]testFunc, error) {
	funcs := map[string]testFunc{}
	for _, path := range paths {
		s, err := readSource(path, fset)
		if err != nil {
			return nil, err
		}
		for _, f := range s.testFuncs() {
			key := pkgDir(path) + ":" + f.name
			if prev, seen := funcs[key]; !seen || f.longest.lines > prev.longest.lines {
				funcs[key] = f
			}
		}
	}
	return funcs, nil
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
