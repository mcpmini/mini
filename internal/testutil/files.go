package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func WriteFile(t testing.TB, path, content string) {
	t.Helper()
	WriteFileBytes(t, path, []byte(content))
}

func WriteFileBytes(t testing.TB, path string, data []byte) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create directory %s: %v", dir, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func ReadFile(t testing.TB, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
