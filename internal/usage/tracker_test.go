package usage_test

import (
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/usage"
)

func TestRecord_incrementsCounters(t *testing.T) {
	tr := usage.New(filepath.Join(t.TempDir(), "usage.json"))
	tr.Record("github", "list_prs", 500, false)
	tr.Record("github", "list_prs", 300, false)
	tr.Record("github", "list_prs", 0, true)

	top := tr.TopTools(0)
	if len(top) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(top))
	}
	e := top[0]
	if e.Calls != 3 {
		t.Errorf("Calls: want 3, got %d", e.Calls)
	}
	if e.Errors != 1 {
		t.Errorf("Errors: want 1, got %d", e.Errors)
	}
	if e.TokensSaved != 800 {
		t.Errorf("TokensSaved: want 800, got %d", e.TokensSaved)
	}
}

func TestRecord_multipleTools(t *testing.T) {
	tr := usage.New(filepath.Join(t.TempDir(), "usage.json"))
	tr.Record("gh", "search_code", 100, false)
	tr.Record("gh", "search_code", 100, false)
	tr.Record("gh", "get_issue", 50, false)

	top := tr.TopTools(0)
	if len(top) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(top))
	}
	if top[0].Tool != "search_code" {
		t.Errorf("expected search_code first (most calls), got %s", top[0].Tool)
	}
}

func TestFlushAndLoad_roundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	tr := usage.New(path)
	tr.Record("svc", "toolA", 1000, false)
	tr.Record("svc", "toolB", 200, true)

	if err := tr.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	tr2 := usage.New(path)
	if err := tr2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	top := tr2.TopTools(0)
	if len(top) != 2 {
		t.Fatalf("expected 2 entries after reload, got %d", len(top))
	}
	found := map[string]bool{}
	for _, e := range top {
		found[e.Tool] = true
	}
	if !found["toolA"] || !found["toolB"] {
		t.Errorf("missing tools after reload: %v", top)
	}
}

func TestLoad_missingFileIsNoOp(t *testing.T) {
	tr := usage.New(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err := tr.Load(); err != nil {
		t.Errorf("Load on missing file should not error, got: %v", err)
	}
	if len(tr.TopTools(0)) != 0 {
		t.Error("expected empty tracker after load of missing file")
	}
}

func TestTopTools_limitsResults(t *testing.T) {
	tr := usage.New(filepath.Join(t.TempDir(), "usage.json"))
	for i := range 5 {
		tr.Record("svc", string(rune('a'+i)), 100, false)
	}

	top := tr.TopTools(3)
	if len(top) != 3 {
		t.Errorf("TopTools(3) should return 3, got %d", len(top))
	}
}

func TestTopTools_sortedByCallsDesc(t *testing.T) {
	tr := usage.New(filepath.Join(t.TempDir(), "usage.json"))
	tr.Record("svc", "low", 0, false)
	tr.Record("svc", "high", 0, false)
	tr.Record("svc", "high", 0, false)
	tr.Record("svc", "high", 0, false)
	tr.Record("svc", "mid", 0, false)
	tr.Record("svc", "mid", 0, false)

	top := tr.TopTools(0)
	if top[0].Tool != "high" || top[1].Tool != "mid" || top[2].Tool != "low" {
		t.Errorf("unexpected order: %v", top)
	}
}

func TestRecord_concurrent(t *testing.T) {
	tr := usage.New(filepath.Join(t.TempDir(), "usage.json"))
	done := make(chan struct{})
	for i := range 10 {
		go func(i int) {
			for range 100 {
				tr.Record("svc", "tool", 10, false)
			}
			done <- struct{}{}
		}(i)
	}
	for range 10 {
		<-done
	}
	top := tr.TopTools(0)
	if len(top) != 1 || top[0].Calls != 1000 {
		t.Errorf("expected 1000 concurrent calls, got: %v", top)
	}
}
