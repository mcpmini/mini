package fileio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWithin(t *testing.T) {
	dir := t.TempDir()
	inner := filepath.Join(dir, "T")
	link := filepath.Join(dir, "link")
	escape := filepath.Join(inner, "escape")
	if err := os.MkdirAll(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inner, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, escape); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		want       bool
	}{
		{"a file not created yet", filepath.Join(inner, "build", "mini"), true},
		{"reached through a symlink to the dir", filepath.Join(link, "build", "mini"), true},
		{"the dir itself", inner, false},
		{"a sibling sharing the name's prefix", filepath.Join(dir, "T-other", "mini"), false},
		{"a symlink inside that points out", filepath.Join(escape, "outside"), false},
		{"dot-dot out of the dir", filepath.Join(inner, "..", "outside"), false},
	} {
		if got := Within(tc.path, inner); got != tc.want {
			t.Errorf("%s: Within(%s) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
}
