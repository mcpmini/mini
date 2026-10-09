package fileio

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
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
	outsideSub := filepath.Join(dir, "other", "sub")
	if err := os.MkdirAll(outsideSub, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(dir, "other", "secret"), "")
	if err := os.Symlink(outsideSub, filepath.Join(inner, "hop")); err != nil {
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
		{"dot-dot out of the dir", inner + "/../outside", false},
		{"dot-dot after a symlink that points out", inner + "/hop/../secret", false},
		{"dot-dot after a symlink that points out, to a file not created yet", inner + "/hop/../notyet", false},
		{"a relative path", "relative/mini", false},
	} {
		if got := Within(tc.path, inner); got != tc.want {
			t.Errorf("%s: Within(%s) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
}
