package response_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/response"
)

func readSlimDoc(t *testing.T, slimPath string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(slimPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("slim file is not valid JSON: %v", err)
	}
	return doc
}

func assertRawPathValid(t *testing.T, slimPath, rawPath string) {
	t.Helper()
	if _, err := os.Stat(rawPath); err != nil {
		t.Fatalf("raw file missing at %s: %v", rawPath, err)
	}
	if !strings.HasSuffix(rawPath, ".raw.json") {
		t.Errorf("raw path should end in .raw.json, got %s", rawPath)
	}
	slimBase := strings.TrimSuffix(filepath.Base(slimPath), ".json")
	rawBase := strings.TrimSuffix(filepath.Base(rawPath), ".raw.json")
	if slimBase != rawBase {
		t.Errorf("base mismatch: slim=%s raw=%s", slimBase, rawBase)
	}
}

func assertRawFile(t *testing.T, slimPath string, slimDoc map[string]any) {
	t.Helper()
	meta, ok := slimDoc["_meta"].(map[string]any)
	if !ok {
		t.Fatal("slim missing _meta")
	}
	rawPath, _ := meta["raw"].(string)
	if rawPath == "" {
		t.Fatal("_meta.raw should be set")
	}
	assertRawPathValid(t, slimPath, rawPath)
	rawBytes, _ := os.ReadFile(rawPath)
	if !json.Valid(rawBytes) {
		t.Fatalf("raw file is not valid JSON")
	}
}

func TestWritePairCreatesSlimAndRawFiles(t *testing.T) {
	store := newTestStore(t)
	data := map[string]any{
		"items":      []any{map[string]any{"number": float64(1), "title": "fix bug", "state": "open"}},
		"totalCount": float64(100),
	}
	slimPath, err := store.WritePair(response.Slimify(data), []byte(`[{"number":1,"title":"fix bug","state":"open"}]`))
	if err != nil {
		t.Fatal(err)
	}
	slimDoc := readSlimDoc(t, slimPath)
	assertRawFile(t, slimPath, slimDoc)
}

func TestWritePairSlimFileIsTimestampNamed(t *testing.T) {
	store := newTestStore(t)
	slim := response.Slimify(map[string]any{"x": "y"})
	path, err := store.WritePair(slim, []byte(`{"x":"y"}`))
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(path)
	if len(name) != 17+len(".json") {
		t.Errorf("slim filename should be 17+5 chars, got %q", name)
	}
}

func TestWritePairEvictsBothFiles(t *testing.T) {
	dir := t.TempDir()
	// 1 MB budget; write files large enough to overflow it so eviction fires.
	store, _ := response.NewStore(response.StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 1, CleanupInterval: time.Hour})
	defer store.Close()

	large := strings.Repeat("x", 600*1024)
	slim := response.Slimify(map[string]any{"items": []any{map[string]any{"x": large}}})
	rawJSON := []byte(`{"raw":"` + large + `"}`)

	path1, _ := store.WritePair(slim, rawJSON)
	rawPath1 := strings.TrimSuffix(path1, ".json") + ".raw.json"

	time.Sleep(2 * time.Millisecond)

	store.WritePair(slim, rawJSON)

	if _, err := os.Stat(path1); !os.IsNotExist(err) {
		t.Error("evicted slim file should be deleted")
	}
	if _, err := os.Stat(rawPath1); !os.IsNotExist(err) {
		t.Error("evicted raw file should be deleted")
	}
}

func assertBothFilesAccounted(t *testing.T, store *response.Store, slimPath string) {
	t.Helper()
	count, used := store.Stats()
	if count != 1 {
		t.Errorf("expected 1 entry after reload, got %d", count)
	}
	rawPath := strings.TrimSuffix(slimPath, ".json") + ".raw.json"
	slimSize := fileSize(t, slimPath)
	rawSize := fileSize(t, rawPath)
	if used < slimSize+rawSize {
		t.Errorf("used bytes %d should include both slim (%d) and raw (%d)", used, slimSize, rawSize)
	}
}

func TestLoadExistingLoadsSlimAndAccountsForRaw(t *testing.T) {
	dir := t.TempDir()
	store, _ := response.NewStore(response.StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 200, CleanupInterval: time.Hour})
	slim := response.Slimify(map[string]any{"x": "y"})
	slimPath, err := store.WritePair(slim, []byte(`{"raw":"data"}`))
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store2, _ := response.NewStore(response.StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 200, CleanupInterval: time.Hour})
	defer store2.Close()
	assertBothFilesAccounted(t, store2, slimPath)
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}
