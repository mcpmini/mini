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

// Dot-directories hold tool state such as agent worktrees, which are whole copies of the tree;
// the go tool skips them for the same reason, and testdata, whose fixtures needn't parse. The walk
// root is exempt so that "." still works.
func isSkippedDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "node_modules"
}

func inWalkedDirs(dirs []string) func(pkgDir string) bool {
	roots := make([]string, len(dirs))
	for i, dir := range dirs {
		roots[i] = workdirRel(dir)
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
	return workdirRel(filepath.Dir(path))
}

func workdirRel(dir string) string {
	wd, err := os.Getwd()
	if err != nil {
		return filepath.ToSlash(filepath.Clean(dir))
	}
	abs := dir
	if !filepath.IsAbs(dir) {
		abs = filepath.Join(wd, dir)
	}
	rel, err := filepath.Rel(wd, abs)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(dir))
	}
	return filepath.ToSlash(rel)
}
