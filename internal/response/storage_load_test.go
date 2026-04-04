package response

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadEntry_recordsRawPairSize(t *testing.T) {
	fixture := loadedStoreWithPair(t)
	if len(fixture.store.files) != 1 {
		t.Fatalf("expected 1 loaded file, got %d", len(fixture.store.files))
	}
	if fixture.store.files[0].rawPath != fixture.rawPath {
		t.Fatalf("rawPath = %q, want %q", fixture.store.files[0].rawPath, fixture.rawPath)
	}
	wantSize := int64(len(fixture.slim) + len(fixture.raw))
	if fixture.store.usedBytes != wantSize {
		t.Fatalf("usedBytes = %d, want %d", fixture.store.usedBytes, wantSize)
	}
}

type loadedStoreFixture struct {
	store   *Store
	rawPath string
	slim    []byte
	raw     []byte
}

func loadedStoreWithPair(t *testing.T) loadedStoreFixture {
	t.Helper()
	dir := t.TempDir()
	base := fmt.Sprintf("%s123", time.Now().UTC().Format(tsLayout))
	slimPath := filepath.Join(dir, base+".json")
	rawPath := filepath.Join(dir, base+".raw.json")
	slim := []byte(`{"ok":true}`)
	raw := []byte(`{"full":"data"}`)
	writeLoadFixture(t, slimPath, slim)
	writeLoadFixture(t, rawPath, raw)
	s := &Store{dir: dir, ttl: time.Hour}
	loadNamedEntry(t, s, filepath.Base(slimPath))
	return loadedStoreFixture{store: s, rawPath: rawPath, slim: slim, raw: raw}
}

func loadNamedEntry(t *testing.T, s *Store, name string) {
	t.Helper()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == name {
			s.loadEntry(entry, time.Now())
		}
	}
}

func writeLoadFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEntry_skipsRawCompanionFiles(t *testing.T) {
	dir := t.TempDir()
	base := fmt.Sprintf("%s123", time.Now().UTC().Format(tsLayout))
	rawPath := filepath.Join(dir, base+".raw.json")
	if err := os.WriteFile(rawPath, []byte(`{"full":"data"}`), 0600); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{dir: dir, ttl: time.Hour}
	for _, entry := range entries {
		s.loadEntry(entry, time.Now())
	}

	if len(s.files) != 0 {
		t.Fatalf("expected raw companion file to be skipped, got %d entries", len(s.files))
	}
}
