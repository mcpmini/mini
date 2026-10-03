package testutil

import (
	"path/filepath"
	"testing"
)

func TestWriteFileCreatesParentsAndTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "fixture")
	WriteFile(t, path, []byte("longer contents"))
	WriteFile(t, path, []byte("short"))
	if got := string(ReadFile(t, path)); got != "short" {
		t.Fatalf("file = %q, want truncated contents %q", got, "short")
	}
}
