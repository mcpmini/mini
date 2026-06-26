package response

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func epochBase(at time.Time) string {
	return fmt.Sprintf("%d", at.UnixMilli())
}

func TestLoadEntry_recordsSize(t *testing.T) {
	dir := t.TempDir()
	base := epochBase(time.Now())
	slimPath := filepath.Join(dir, base+".json")
	data := []byte(`{"ok":true}`)
	if err := os.WriteFile(slimPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	s := &Store{dir: dir, ttl: time.Hour}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == filepath.Base(slimPath) {
			s.loadEntry(e, time.Now())
		}
	}
	if len(s.files) != 1 {
		t.Fatalf("expected 1 loaded file, got %d", len(s.files))
	}
	if s.usedBytes != int64(len(data)) {
		t.Fatalf("usedBytes = %d, want %d", s.usedBytes, len(data))
	}
}

func TestLoadEntry_skipsRawCompanionFiles(t *testing.T) {
	dir := t.TempDir()
	base := epochBase(time.Now())
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
