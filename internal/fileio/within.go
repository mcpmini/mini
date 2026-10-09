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

func resolved(path string) string {
	// Symlinks resolve before ".." is applied, as the OS does: link/.. is link's target's parent.
	if target, err := filepath.EvalSymlinks(path); err == nil {
		return absolute(target)
	}
	parent := filepath.Dir(path)
	if parent == path {
		return absolute(path)
	}
	return filepath.Join(resolved(parent), filepath.Base(path))
}

func absolute(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
