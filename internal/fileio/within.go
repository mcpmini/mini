package fileio

import (
	"os"
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

// Each part resolves before the next applies, as the OS walks a path: link/.. is the parent of
// link's target, even when what follows doesn't exist yet.
func resolved(path string) string {
	if !filepath.IsAbs(path) {
		if wd, err := os.Getwd(); err == nil {
			path = wd + string(filepath.Separator) + path
		}
	}
	volume := filepath.VolumeName(path)
	current := volume + string(filepath.Separator)
	for _, part := range strings.Split(path[len(volume):], string(filepath.Separator)) {
		switch part {
		case "", ".":
		case "..":
			current = filepath.Dir(current)
		default:
			current = filepath.Join(current, part)
			if target, err := filepath.EvalSymlinks(current); err == nil {
				current = target
			}
		}
	}
	return current
}
