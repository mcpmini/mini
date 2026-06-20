package testutil

import (
	"os"
	"runtime"
	"testing"
)

// ShortTempDir creates a temp directory under /tmp rather than t.TempDir(), which is necessary
// because macOS caps Unix socket paths at 104 bytes and t.TempDir() paths routinely exceed that.
func ShortTempDir(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = ""
	}
	dir, err := os.MkdirTemp(base, "mini")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) }) //nolint:errcheck
	return dir
}
