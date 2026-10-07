package fileio

import (
	"path/filepath"
	"strings"
)

// Within reports whether path is inside dir, not dir itself. Both are compared with symlinks
// resolved as far as they exist, so a symlink inside dir can't point out of it, and macOS's
// symlinked temp directory (/var → /private/var) matches either spelling.
func Within(path, dir string) bool {
	rel, err := filepath.Rel(resolved(dir), resolved(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// A path that doesn't exist yet keeps its missing tail and resolves the rest.
func resolved(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(resolved(parent), filepath.Base(path))
}
