package main

import (
	"os"
	"path/filepath"
	"strings"
)

type sourceFiles struct {
	production []string
	tests      []string
}

func collectFiles(dirs []string) (sourceFiles, error) {
	var files sourceFiles
	for _, dir := range dirs {
		if err := walkDir(dir, &files); err != nil {
			return sourceFiles{}, err
		}
	}
	return files, nil
}

func walkDir(root string, files *sourceFiles) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && path != root && isSkippedDir(info.Name()) {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			files.add(path)
		}
		return nil
	})
}

func (f *sourceFiles) add(path string) {
	switch {
	case !strings.HasSuffix(path, ".go"):
	case strings.HasSuffix(path, "_test.go"):
		f.tests = append(f.tests, path)
	default:
		f.production = append(f.production, path)
	}
}

// Dot-directories hold tool state such as agent worktrees, which are whole copies of the tree, and
// testdata holds fixtures that needn't parse; the go tool skips both.
func isSkippedDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "node_modules"
}

func inWalkedDirs(dirs []string) func(pkgDir string) bool {
	roots := make([]string, len(dirs))
	for i, dir := range dirs {
		roots[i] = moduleRel(dir)
	}
	return func(pkgDir string) bool {
		for _, root := range roots {
			if root == "." || pkgDir == root || strings.HasPrefix(pkgDir, root+"/") {
				return true
			}
		}
		return false
	}
}

func pkgDir(path string) string {
	return moduleRel(filepath.Dir(path))
}

// Baseline keys are rooted at the module so that a run from a subdirectory, or on a directory
// spelled absolute or with ../, matches the same entries.
func moduleRel(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(dir))
	}
	rel, err := filepath.Rel(moduleRoot(), abs)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(dir))
	}
	return filepath.ToSlash(rel)
}

// moduleRoot falls back to the working directory outside a module.
func moduleRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			return wd
		}
	}
}
