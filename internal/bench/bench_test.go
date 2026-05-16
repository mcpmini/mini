package bench_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/bench"
	"github.com/mcpmini/mini/internal/config"
	minidefaults "github.com/mcpmini/mini/internal/defaults"
)

var defaults = bench.DefaultProjectionDefaults()

func prListRaw(t *testing.T) []byte {
	t.Helper()
	raw, _ := json.Marshal([]any{
		map[string]any{
			"id": 1, "title": "PR title",
			"body":     "This is a very long PR body that describes a lot of things in detail. " + repeat("more text ", 50),
			"node_id":  "abc123",
			"url":      "https://api.github.com/...",
			"html_url": "https://github.com/...",
			"diff_url": "https://github.com/...diff",
			"_links":   map[string]any{"self": "https://...", "html": "https://..."},
		},
		map[string]any{"id": 2, "title": "Another PR", "body": "Short body", "node_id": "def456"},
	})
	return raw
}

func TestMeasure_rawIsLargestTokenCount(t *testing.T) {
	cfg := &config.ProjectionConfig{
		Include:      []string{"id", "title", "body"},
		StringLimits: map[string]int{"body": 100},
		StripMarkup: true,
	}
	results := bench.Measure(bench.Case{Server: "github", Tool: "list_prs", Raw: prListRaw(t), ProjConfig: cfg}, defaults)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Tokens < results[1].Tokens {
		t.Errorf("raw (%d) should be >= projected (%d)", results[0].Tokens, results[1].Tokens)
	}
	if results[1].Tokens < results[2].Tokens {
		t.Errorf("projected (%d) should be >= stripped (%d)", results[1].Tokens, results[2].Tokens)
	}
}

func TestMeasure_projectionReducesTokens(t *testing.T) {
	body := repeat("This is the PR body content with lots of HTML markup <b>bold</b> and <i>italic</i> text. ", 20)
	raw, _ := json.Marshal(map[string]any{
		"number": 1, "title": "Test PR", "body": body,
		"node_id": "abc123", "url": "https://api.github.com/test",
		"html_url": "https://github.com/test", "diff_url": "https://github.com/test.diff",
		"labels": []any{
			map[string]any{"id": 1, "name": "bug", "node_id": "LA_abc", "url": "https://api.github.com/labels/1"},
			map[string]any{"id": 2, "name": "fix", "node_id": "LA_def", "url": "https://api.github.com/labels/2"},
		},
		"_links": map[string]any{"self": "https://...", "html": "https://...", "commits": "https://..."},
	})
	cfg := &config.ProjectionConfig{
		Include:       []string{"number", "title", "body", "labels"},
		StringLimits:  map[string]int{"body": 200},
		ExcludeAlways: []string{"node_id", "url", "_links"},
	}
	results := bench.Measure(bench.Case{Server: "test", Tool: "list_prs", Raw: raw, ProjConfig: cfg}, defaults)
	if results[1].Tokens >= results[0].Tokens {
		t.Errorf("projection should reduce tokens: raw=%d projected=%d", results[0].Tokens, results[1].Tokens)
	}
}

func TestMeasure_modesLabelled(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"id": 1, "title": "test"})
	results := bench.Measure(bench.Case{Server: "s", Tool: "t", Raw: raw, ProjConfig: nil}, defaults)
	modes := make(map[string]bool)
	for _, r := range results {
		modes[r.Mode] = true
		if r.Server != "s" || r.Tool != "t" {
			t.Errorf("server/tool not propagated: %+v", r)
		}
	}
	for _, m := range []string{"raw", "projected", "stripped"} {
		if !modes[m] {
			t.Errorf("missing mode %q in results", m)
		}
	}
}

func TestMeasure_invalidJSON_returnsRawOnly(t *testing.T) {
	results := bench.Measure(bench.Case{Server: "s", Tool: "t", Raw: []byte("not json"), ProjConfig: nil}, defaults)
	if len(results) != 1 || results[0].Mode != "raw" {
		t.Errorf("invalid JSON should return raw result only, got %+v", results)
	}
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	benchDir := t.TempDir()
	writeFixture(t, benchDir, "github", "list_pull_requests", `[{"number":1,"title":"Test"}]`)
	writeFixture(t, benchDir, "github", "list_issues", `[{"number":2,"title":"Bug"}]`)
	writeFixture(t, benchDir, "linear", "list_issues", `{"nodes":[{"id":"abc"}]}`)
	writeProjection(t, benchDir, "github", "list_pull_requests:\n  include: [number, title]\n  string_limits:\n    body: 300\n")
	return benchDir
}

func TestLoadFixtures_loadsExpectedCases(t *testing.T) {
	cases, err := bench.LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}
	if len(cases) != 3 {
		t.Errorf("expected 3 cases, got %d", len(cases))
	}
	byKey := make(map[string]bench.Case)
	for _, c := range cases {
		byKey[c.Server+"/"+c.Tool] = c
	}
	assertFixtureCases(t, byKey)
}

func assertFixtureCases(t *testing.T, byKey map[string]bench.Case) {
	t.Helper()
	ghPR, ok := byKey["github/list_pull_requests"]
	if !ok {
		t.Fatal("missing github/list_pull_requests")
	}
	if ghPR.ProjConfig == nil {
		t.Error("expected projection config for github/list_pull_requests")
	}
	if _, ok := byKey["github/list_issues"]; !ok {
		t.Error("missing github/list_issues")
	}
	if _, ok := byKey["linear/list_issues"]; !ok {
		t.Error("missing linear/list_issues")
	}
}

func TestLoadFixtures_missingFixturesDir_returnsEmpty(t *testing.T) {
	cases, err := bench.LoadFixtures("/nonexistent/path")
	if err != nil {
		t.Errorf("expected nil error for missing fixtures dir, got %v", err)
	}
	if len(cases) != 0 {
		t.Errorf("expected 0 cases, got %d", len(cases))
	}
}

func tokensByMode(results []bench.Result) (raw, projected, stripped int) {
	for _, r := range results {
		switch r.Mode {
		case "raw":
			raw = r.Tokens
		case "projected":
			projected = r.Tokens
		case "stripped":
			stripped = r.Tokens
		}
	}
	return
}

func TestBenchmark_githubPRs_bundledProjectionApplies(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	fixture := filepath.Join(root, "benchmarks", "fixtures", "github", "list_pull_requests.json")
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Skip("fixture not available:", err)
	}
	projData := minidefaults.ProjectionFor("github")
	if projData == nil {
		t.Fatal("bundled github projection not found")
	}
	var projMap map[string]*config.ProjectionConfig
	if err := yaml.Unmarshal(projData, &projMap); err != nil {
		t.Fatalf("parse projection: %v", err)
	}
	c := bench.Case{Server: "github", Tool: "list_pull_requests", Raw: raw, ProjConfig: projMap["list_pull_requests"]}
	rawT, projT, _ := tokensByMode(bench.Measure(c, bench.DefaultProjectionDefaults()))
	if rawT == 0 {
		t.Fatal("fixture produced no raw tokens")
	}
	if projT >= rawT {
		t.Errorf("bundled projection should reduce tokens: raw=%d projected=%d", rawT, projT)
	}
	t.Logf("raw=%d projected=%d reduction=%.1f%%", rawT, projT, float64(rawT-projT)/float64(rawT)*100)
}

func writeFixture(t *testing.T, benchDir, server, tool, content string) {
	t.Helper()
	dir := filepath.Join(benchDir, "fixtures", server)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tool+".json"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeProjection(t *testing.T, benchDir, server, content string) {
	t.Helper()
	dir := filepath.Join(benchDir, "projections")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, server+".yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
