package response_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/response"
)

func newTestStore(t *testing.T) *response.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := response.NewStore(response.StoreConfig{Dir: dir, TTL: 15 * time.Minute, BudgetMB: 200, CleanupInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestInlineSmallResponse(t *testing.T) {
	store := newTestStore(t)
	builder := response.NewBuilder(store)

	raw := json.RawMessage(`{"status":"ok","id":"abc"}`)
	data := map[string]any{"status": "ok", "id": "abc"}

	env, _, err := builder.Build(response.BuildParams{Server: "ci", Tool: "getStatus", Raw: raw, Summary: data})
	if err != nil {
		t.Fatal(err)
	}

	if env.File != nil {
		t.Error("inline responses should not have a file path")
	}
}

func TestFileWrittenWhenProjectionApplied(t *testing.T) {
	store := newTestStore(t)
	builder := response.NewBuilder(store)

	raw := json.RawMessage(`{"status":"ok","body":"secret content"}`)
	data := map[string]any{"status": "ok"}

	env, _, err := builder.Build(response.BuildParams{Server: "ci", Tool: "getPage", Raw: raw, Summary: data, Excluded: []string{"body"}})
	if err != nil {
		t.Fatal(err)
	}

	if env.File == nil {
		t.Fatal("expected raw file path when fields were excluded")
	}
	if _, err := os.Stat(*env.File); os.IsNotExist(err) {
		t.Errorf("response file does not exist: %s", *env.File)
	}
	if env.Data == nil {
		t.Error("expected data to be inlined even when file is written")
	}
}


func TestExcludedKeys(t *testing.T) {
	store := newTestStore(t)
	builder := response.NewBuilder(store)

	raw := json.RawMessage(`{"a":1,"b":2,"c":3}`)
	data := map[string]any{"a": 1}
	excluded := []string{"b", "c"}

	env, _, err := builder.Build(response.BuildParams{Server: "s", Tool: "t", Raw: raw, Summary: data, Excluded: excluded})
	if err != nil {
		t.Fatal(err)
	}

	if len(env.Excluded) != 2 {
		t.Errorf("expected 2 excluded keys, got %v", env.Excluded)
	}
}

func TestOmittedFields(t *testing.T) {
	store := newTestStore(t)
	builder := response.NewBuilder(store)

	raw := json.RawMessage(`{"summary":"short","body":"` + strings.Repeat("x", 500) + `"}`)
	data := map[string]any{"summary": "short", "body": strings.Repeat("x", 50)}
	truncated := []projection.Truncation{{JQPath: ".body", Chars: 450}}

	env, _, err := builder.Build(response.BuildParams{
		Server: "ci", Tool: "getPage", Raw: raw, Summary: data, Truncated: truncated,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(env.Truncated) != 1 || env.Truncated[0].JQPath != ".body" || env.Truncated[0].Chars != 450 {
		t.Errorf("expected truncated=[{.body 450}], got %v", env.Truncated)
	}
	if env.File == nil {
		t.Error("expected file written when a field was truncated")
	}
}

func TestCallStatsReduction(t *testing.T) {
	store := newTestStore(t)
	builder := response.NewBuilder(store)

	raw := json.RawMessage(`{"status":"ok","body":"this is a large response with lots of text content that exceeds the threshold"}`)
	data := map[string]any{"status": "ok"}

	_, stats, err := builder.Build(response.BuildParams{Server: "ci", Tool: "getPage", Raw: raw, Summary: data})
	if err != nil {
		t.Fatal(err)
	}

	if stats.RawTokens == 0 {
		t.Error("expected non-zero raw token count")
	}
	if stats.SummaryTokens >= stats.RawTokens {
		t.Errorf("summary should be smaller: summary=%d raw=%d", stats.SummaryTokens, stats.RawTokens)
	}
	if stats.ReductionPct() <= 0 {
		t.Errorf("expected positive reduction, got %.1f%%", stats.ReductionPct())
	}
}

func TestBuildError(t *testing.T) {
	env := response.BuildError("auth_expired", "Token expired", true, "Run: mini auth refresh ci")
	if env.Error != "auth_expired" {
		t.Errorf("unexpected error code: %s", env.Error)
	}
	if !env.Retryable {
		t.Error("expected retryable=true")
	}
}

func TestTokenEstimation(t *testing.T) {
	tokens := response.EstimateTokensRaw([]byte(`{"a":"b"}`))
	if tokens == 0 {
		t.Error("expected non-zero token estimate")
	}
}

func TestTimestampFilenames(t *testing.T) {
	store := newTestStore(t)
	raw, _ := json.Marshal(map[string]any{"x": "y"})
	path, err := store.WriteRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(path)
	if len(name) != 17+len(".json") {
		t.Errorf("unexpected filename format: %s", name)
	}
	if filepath.Ext(name) != ".json" {
		t.Errorf("expected .json extension: %s", name)
	}
}

func tsFilename(at time.Time) string {
	return fmt.Sprintf("%s%03d.json", at.UTC().Format("20060102150405"), at.UTC().Nanosecond()/1_000_000)
}

func TestLoadExistingSkipsExpired(t *testing.T) {
	dir := t.TempDir()
	expired := tsFilename(time.Now().Add(-2 * time.Hour))
	os.WriteFile(filepath.Join(dir, expired), []byte(`{"old":true}`), 0600)
	fresh := tsFilename(time.Now())
	os.WriteFile(filepath.Join(dir, fresh), []byte(`{"new":true}`), 0600)

	store, _ := response.NewStore(response.StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 200, CleanupInterval: time.Hour})
	defer store.Close()

	count, _ := store.Stats()
	if count != 1 {
		t.Errorf("expected 1 survivor after load, got %d", count)
	}
	if _, err := os.Stat(filepath.Join(dir, expired)); !os.IsNotExist(err) {
		t.Error("expired file should have been deleted")
	}
}

func TestStoreDiskBudget(t *testing.T) {
	dir := t.TempDir()
	store, _ := response.NewStore(response.StoreConfig{Dir: dir, TTL: time.Hour, BudgetMB: 1, CleanupInterval: time.Hour})

	raw := []byte(`{"data":"` + strings.Repeat("x", 600*1024) + `"}`)
	for range 5 {
		store.WriteRaw(raw)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) > 2 {
		t.Errorf("expected at most 2 files under budget, got %d", len(entries))
	}
}
