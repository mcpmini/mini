//go:build test

package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeServerFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	serversDir := filepath.Join(dir, "servers")
	if err := os.MkdirAll(serversDir, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(serversDir, name)
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustFingerprint(t *testing.T, dir string) map[string]string {
	t.Helper()
	fp, err := fingerprintServerFiles(dir)
	if err != nil {
		t.Fatalf("fingerprintServerFiles: %v", err)
	}
	return fp
}

func TestFingerprintServerFiles(t *testing.T) {
	t.Run("missing servers dir yields empty fingerprint", func(t *testing.T) {
		fp := mustFingerprint(t, t.TempDir())
		if len(fp) != 0 {
			t.Errorf("expected empty fingerprint, got %v", fp)
		}
	})

	t.Run("covers server yaml and proj yaml but not other files", func(t *testing.T) {
		dir := t.TempDir()
		writeServerFile(t, dir, "svc.yaml", "name: svc\n")
		writeServerFile(t, dir, "svc.proj.yaml", "tool:\n  include_only: [a]\n")
		writeServerFile(t, dir, "notes.txt", "ignored")
		fp := mustFingerprint(t, dir)
		if len(fp) != 2 {
			t.Errorf("expected 2 entries, got %v", fp)
		}
	})

	t.Run("same size content change changes hash", func(t *testing.T) {
		dir := t.TempDir()
		p := writeServerFile(t, dir, "svc.proj.yaml", "tool:\n  include_only: [a]\n")
		before := mustFingerprint(t, dir)
		if err := os.WriteFile(p, []byte("tool:\n  include_only: [b]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		after := mustFingerprint(t, dir)
		if before[p] == after[p] {
			t.Error("expected hash to change on same-size content edit")
		}
	})

	t.Run("identical content yields identical fingerprint", func(t *testing.T) {
		dir := t.TempDir()
		writeServerFile(t, dir, "svc.yaml", "name: svc\n")
		if a, b := mustFingerprint(t, dir), mustFingerprint(t, dir); !reflect.DeepEqual(a, b) {
			t.Errorf("expected stable fingerprint, got %v vs %v", a, b)
		}
	})

	t.Run("unreadable file returns error", func(t *testing.T) {
		dir := t.TempDir()
		p := writeServerFile(t, dir, "svc.yaml", "name: svc\n")
		if err := os.Chmod(p, 0000); err != nil {
			t.Skip("cannot make file unreadable:", err)
		}
		t.Cleanup(func() { os.Chmod(p, 0600) }) //nolint:errcheck
		if _, err := fingerprintServerFiles(dir); err == nil {
			t.Error("expected error for unreadable file")
		}
	})
}

func TestChangedPaths(t *testing.T) {
	tests := []struct {
		name string
		prev map[string]string
		curr map[string]string
		want []string
	}{
		{name: "no change", prev: map[string]string{"a": "1"}, curr: map[string]string{"a": "1"}, want: nil},
		{name: "nil prev reports all current", prev: nil, curr: map[string]string{"a": "1", "b": "2"}, want: []string{"a", "b"}},
		{name: "added file", prev: map[string]string{"a": "1"}, curr: map[string]string{"a": "1", "b": "2"}, want: []string{"b"}},
		{name: "removed file", prev: map[string]string{"a": "1", "b": "2"}, curr: map[string]string{"a": "1"}, want: []string{"b"}},
		{name: "modified hash", prev: map[string]string{"a": "1"}, curr: map[string]string{"a": "9"}, want: []string{"a"}},
		{name: "rename is add plus remove", prev: map[string]string{"a": "1"}, curr: map[string]string{"b": "1"}, want: []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := changedPaths(tt.prev, tt.curr); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("changedPaths(%v, %v) = %v, want %v", tt.prev, tt.curr, got, tt.want)
			}
		})
	}
}
