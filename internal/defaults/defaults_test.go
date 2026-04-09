package defaults

import (
	"bytes"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectionForReturnsBundledProjection(t *testing.T) {
	entries, err := fs.ReadDir(FS, "projections")
	if err != nil {
		t.Fatalf("ReadDir projections: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected bundled projections")
	}

	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		want, err := FS.ReadFile("projections/" + entry.Name())
		if err != nil {
			t.Fatalf("ReadFile %s: %v", entry.Name(), err)
		}
		got := ProjectionFor(name)
		if !bytes.Equal(got, want) {
			t.Errorf("ProjectionFor(%q) did not return bundled contents", name)
		}
	}
}

func TestProjectionForUnknownServerReturnsNil(t *testing.T) {
	if got := ProjectionFor("does-not-exist"); got != nil {
		t.Fatalf("expected nil for unknown server, got %q", string(got))
	}
}

func TestPermissionsForReturnsBundledPermissions(t *testing.T) {
	entries, err := fs.ReadDir(FS, "permissions")
	if err != nil {
		t.Fatalf("ReadDir permissions: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected bundled permissions")
	}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		got := PermissionsFor(name)
		if got == nil {
			t.Errorf("PermissionsFor(%q) returned nil", name)
		}
	}
}

func TestPermissionsForUnknownServerReturnsNil(t *testing.T) {
	if got := PermissionsFor("does-not-exist"); got != nil {
		t.Fatalf("expected nil for unknown server, got %q", string(got))
	}
}
