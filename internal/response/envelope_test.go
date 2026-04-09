package response_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/response"
)

func newTestStore(t *testing.T) *response.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := response.NewStore(response.StoreConfig{Dir: dir, TTL: 15*time.Minute, BudgetMB: 200, CleanupInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestInlineSmallResponse(t *testing.T) {
	store := newTestStore(t)
	builder := response.NewBuilder(store, 500)

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
	builder := response.NewBuilder(store, 0)

	raw := json.RawMessage(`{"status":"ok","body":"secret content"}`)
	data := map[string]any{"status": "ok"}

	env, _, err := builder.Build(response.BuildParams{Server: "ci", Tool: "getPage", Raw: raw, Summary: data, Elided: []string{"body"}})
	if err != nil {
		t.Fatal(err)
	}

	if env.File == nil {
		t.Fatal("expected raw file path when fields were elided")
	}
	if _, err := os.Stat(*env.File); os.IsNotExist(err) {
		t.Errorf("response file does not exist: %s", *env.File)
	}
	// data is always inlined regardless of file write
	if env.Data == nil {
		t.Error("expected data to be inlined even when file is written")
	}
}

func TestFileWrittenWhenThresholdExceeded(t *testing.T) {
	store := newTestStore(t)
	builder := response.NewBuilder(store, 5) // threshold of 5 tokens

	// Large response (>> 5 tokens) with no projection should still write to file.
	raw := json.RawMessage(`{"status":"ok","body":"` + strings.Repeat("x", 200) + `"}`)
	data := map[string]any{"status": "ok", "body": strings.Repeat("x", 200)}

	env, _, err := builder.Build(response.BuildParams{Server: "ci", Tool: "getPage", Raw: raw, Summary: data})
	if err != nil {
		t.Fatal(err)
	}

	if env.File == nil {
		t.Error("expected file written when raw token count exceeds threshold")
	}
}

func TestNoFileWhenBelowThreshold(t *testing.T) {
	store := newTestStore(t)
	builder := response.NewBuilder(store, 500) // high threshold

	raw := json.RawMessage(`{"id":1}`)
	data := map[string]any{"id": 1}

	env, _, err := builder.Build(response.BuildParams{Server: "ci", Tool: "get", Raw: raw, Summary: data})
	if err != nil {
		t.Fatal(err)
	}

	if env.File != nil {
		t.Error("no file should be written when response is below threshold and no projection applied")
	}
}

func TestElidedKeys(t *testing.T) {
	store := newTestStore(t)
	builder := response.NewBuilder(store, 5)

	raw := json.RawMessage(`{"a":1,"b":2,"c":3}`)
	data := map[string]any{"a": 1}
	elided := []string{"b", "c"}

	env, _, err := builder.Build(response.BuildParams{Server: "s", Tool: "t", Raw: raw, Summary: data, Elided: elided})
	if err != nil {
		t.Fatal(err)
	}

	if len(env.Elided) != 2 {
		t.Errorf("expected 2 elided keys, got %v", env.Elided)
	}
}

func TestTruncatedKeys(t *testing.T) {
	store := newTestStore(t)
	builder := response.NewBuilder(store, 0)

	raw := json.RawMessage(`{"summary":"short","body":"` + strings.Repeat("x", 500) + `"}`)
	data := map[string]any{"summary": "short", "body": strings.Repeat("x", 50)}
	truncated := map[string]int{"body": 450}

	env, _, err := builder.Build(response.BuildParams{
		Server: "ci", Tool: "getPage", Raw: raw, Summary: data, Truncated: truncated,
	})
	if err != nil {
		t.Fatal(err)
	}

	if env.Truncated["body"] != 450 {
		t.Errorf("expected truncated[body]=450, got %v", env.Truncated)
	}
	// truncation counts as projection applied — raw file should exist
	if env.File == nil {
		t.Error("expected file written when truncation applied")
	}
}

func TestCallStatsReduction(t *testing.T) {
	store := newTestStore(t)
	builder := response.NewBuilder(store, 5)

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
	// name must be 17-char timestamp (YYYYMMDDHHMMSSMMM) + ".json"
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
	// Use 1 MB budget; write files large enough (600 KB each) to overflow.
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
