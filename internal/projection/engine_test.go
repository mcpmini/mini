package projection_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
)

var defaultLimits = &projection.Defaults{
	StringLimit:        1000,
	DepthLimit:         3,
	ContentFields:      []string{"body", "description", "text", "readme", "content", "message", "summary", "patch"},
	AutoStripThreshold: 500,
}

func TestPassthroughOnNoConfig(t *testing.T) {
	value := map[string]any{"status": "ok", "id": "abc"}
	result := projection.Apply(value, nil, defaultLimits)

	if result.Summary == nil {
		t.Fatal("expected non-nil summary")
	}
	m := result.Summary.(map[string]any)
	if m["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", m["status"])
	}
}

func TestIncludeFilter(t *testing.T) {
	cfg := &config.ProjectionConfig{Include: []string{"status", "branch"}}
	value := map[string]any{
		"status": "failed",
		"branch": "main",
		"env":    map[string]any{"SECRET": "password"},
	}

	result := projection.Apply(value, cfg, defaultLimits)
	m := result.Summary.(map[string]any)

	if _, ok := m["env"]; ok {
		t.Error("env should be excluded by include filter")
	}
	if m["status"] != "failed" {
		t.Errorf("expected status=failed, got %v", m["status"])
	}
}

func TestExcludeAlways(t *testing.T) {
	cfg := &config.ProjectionConfig{ExcludeAlways: []string{"env", "pipeline.configuration"}}
	value := map[string]any{
		"status": "ok",
		"env":    map[string]any{"SECRET": "123"},
	}

	result := projection.Apply(value, cfg, defaultLimits)
	m := result.Summary.(map[string]any)

	if _, ok := m["env"]; ok {
		t.Error("env should be excluded")
	}
}

func TestArrayLimit(t *testing.T) {
	t.Run("per-projection limit truncates with sentinel", func(t *testing.T) {
		value := map[string]any{"steps": []any{"a", "b", "c", "d", "e"}}
		cfg := &config.ProjectionConfig{ArrayLimits: map[string]int{"steps": 2}}
		result := projection.Apply(value, cfg, &projection.Defaults{StringLimit: 1000, DepthLimit: 5})
		m := result.Summary.(map[string]any)
		steps := m["steps"].([]any)
		if len(steps) != 3 { // 2 items + sentinel
			t.Errorf("expected 2 items + sentinel = 3, got %d", len(steps))
		}
		if steps[2] != "...+3 more" {
			t.Errorf("expected sentinel, got %v", steps[2])
		}
	})

	t.Run("no limit returns all items", func(t *testing.T) {
		value := map[string]any{"steps": []any{"a", "b", "c", "d", "e"}}
		result := projection.Apply(value, nil, &projection.Defaults{StringLimit: 1000, DepthLimit: 5})
		m := result.Summary.(map[string]any)
		steps := m["steps"].([]any)
		if len(steps) != 5 {
			t.Errorf("expected all 5 items with no limit, got %d", len(steps))
		}
	})
}

func TestStringTruncation(t *testing.T) {
	// 2000 bytes of 'x' — no word/sentence boundaries, so truncation is exact at 100.
	long := strings.Repeat("x", 2000)
	value := map[string]any{"body": long}

	result := projection.Apply(value, nil, &projection.Defaults{StringLimit: 100, DepthLimit: 5})
	m := result.Summary.(map[string]any)

	want := strings.Repeat("x", 100)
	if m["body"] != want {
		t.Errorf("body = %q, want %q", m["body"], want)
	}
	if len(result.Omitted) != 1 || result.Omitted[0].Bytes != 1900 {
		t.Errorf("omitted = %v, want [{.body 1900}]", result.Omitted)
	}
}

// TestDepthLimit verifies that depth is counted from 0 (top-level = depth 0).
// With depthLimit=2: depth 0 (top-level) and depth 1 (a) are projected,
// but depth 2 (a.b) returns "[depth limit reached]".
func TestDepthLimit(t *testing.T) {
	value := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": map[string]any{
					"deep": "value",
				},
			},
		},
	}

	result := projection.Apply(value, nil, &projection.Defaults{DepthLimit: 2, StringLimit: 1000})
	m := result.Summary.(map[string]any)
	a := m["a"].(map[string]any)

	if a["b"] != "[depth limit reached]" {
		t.Errorf("expected a.b=[depth limit reached] at depth 2, got %v", a["b"])
	}
}

func TestElidedKeys(t *testing.T) {
	cfg := &config.ProjectionConfig{Include: []string{"status"}}
	value := map[string]any{
		"status": "ok",
		"env":    "secret",
		"body":   "long text",
	}

	result := projection.Apply(value, cfg, defaultLimits)
	if len(result.ElidedKeys) < 2 {
		t.Errorf("expected at least 2 elided keys, got %v", result.ElidedKeys)
	}
}

func TestPassthroughFields(t *testing.T) {
	cfg := &config.ProjectionConfig{
		Include:     []string{"status"},
		Passthrough: []string{"cursor", "next_page"},
	}
	value := map[string]any{
		"status":    "ok",
		"env":       "secret",
		"cursor":    "page2-token",
		"next_page": "https://example.com/next",
	}

	result := projection.Apply(value, cfg, defaultLimits)
	if result.Passthrough["cursor"] != "page2-token" {
		t.Errorf("cursor should be in passthrough: %v", result.Passthrough)
	}
}

func TestIncludeFilterOnlyTopLevel_excludesNonListed(t *testing.T) {
	cfg := &config.ProjectionConfig{Include: []string{"issues", "totalCount"}}
	value := map[string]any{
		"issues":     []any{map[string]any{"number": 1, "title": "Bug report", "state": "open"}},
		"totalCount": 42,
		"pageInfo":   map[string]any{"hasNextPage": true},
	}
	result := projection.Apply(value, cfg, defaultLimits)
	m := result.Summary.(map[string]any)
	if _, ok := m["pageInfo"]; ok {
		t.Error("pageInfo should be excluded by include filter")
	}
	if m["totalCount"] == nil {
		t.Error("totalCount should be included")
	}
}

func TestIncludeFilterOnlyTopLevel_preservesNestedFields(t *testing.T) {
	cfg := &config.ProjectionConfig{Include: []string{"issues"}}
	value := map[string]any{
		"issues": []any{map[string]any{"number": 1, "title": "Bug report", "state": "open"}},
	}
	result := projection.Apply(value, cfg, defaultLimits)
	m := result.Summary.(map[string]any)
	issues, ok := m["issues"].([]any)
	if !ok || len(issues) == 0 {
		t.Fatal("issues array should be present and non-empty")
	}
	issue := issues[0].(map[string]any)
	for _, key := range []string{"number", "title", "state"} {
		if issue[key] == nil {
			t.Errorf("%s inside issue should not be stripped by include filter", key)
		}
	}
}

func namedArrayLimitsResult(t *testing.T) map[string]any {
	t.Helper()
	cfg := &config.ProjectionConfig{ArrayLimits: map[string]int{"issues": 5}}
	value := map[string]any{
		"issues":     []any{1, 2, 3, 4, 5, 6, 7},
		"other_list": []any{"a", "b", "c", "d", "e"},
	}
	limits := &projection.Defaults{StringLimit: 1000, DepthLimit: 5,
		ContentFields: []string{}, AutoStripThreshold: 0}
	return projection.Apply(value, cfg, limits).Summary.(map[string]any)
}

func TestNamedArrayLimit_namedLimitApplied(t *testing.T) {
	m := namedArrayLimitsResult(t)
	issues := m["issues"].([]any)
	if len(issues) != 6 {
		t.Errorf("expected 5 items + sentinel = 6, got %d", len(issues))
	}
	if issues[5] != "...+2 more" {
		t.Errorf("expected sentinel ...+2 more, got %v", issues[5])
	}
}

func TestNamedArrayLimit_unlimitedForOtherFields(t *testing.T) {
	m := namedArrayLimitsResult(t)
	other := m["other_list"].([]any)
	if len(other) != 5 {
		t.Errorf("expected all 5 items (no global limit), got %d", len(other))
	}
}

func TestNamedStringLimit(t *testing.T) {
	cfg := &config.ProjectionConfig{
		StringLimits: map[string]int{"body": 50},
	}
	long := "word word word word word word word word word word word word word word"
	value := map[string]any{
		"body":  long,
		"title": long,
	}

	result := projection.Apply(value, cfg, &projection.Defaults{StringLimit: 1000, DepthLimit: 5})
	m := result.Summary.(map[string]any)

	// "word word ... word" with limit 50: truncateAtBoundary finds space at [49] → 49 chars kept.
	// long = 14 "word"s with 13 spaces = 69 chars total; removed = 69 - 49 = 20.
	wantBody := "word word word word word word word word word word"
	if m["body"].(string) != wantBody {
		t.Errorf("body = %q, want %q", m["body"], wantBody)
	}
	if len(result.Omitted) != 1 || result.Omitted[0].Path != ".body" || result.Omitted[0].Bytes != 20 {
		t.Errorf("omitted = %v, want [{.body 20}]", result.Omitted)
	}
	// title has no named limit — should pass through untruncated
	if m["title"].(string) != long {
		t.Errorf("title should not be truncated")
	}
}

func slimModeResult(t *testing.T) map[string]any {
	t.Helper()
	cfg := &config.ProjectionConfig{Mode: "slim"}
	long := strings.Repeat("x", 500)
	value := map[string]any{
		"body":   long,
		"items":  []any{1, 2, 3, 4, 5, 6, 7},
		"nested": map[string]any{"a": map[string]any{"b": map[string]any{"c": "deep"}}},
	}
	return projection.Apply(value, cfg, defaultLimits).Summary.(map[string]any)
}

func TestSlimMode_stringAndArrayLimits(t *testing.T) {
	m := slimModeResult(t)
	// 500 bytes of 'x' with slim limit 200 — no boundaries → exact 200 chars, removed 300.
	wantBody := strings.Repeat("x", 200)
	if m["body"].(string) != wantBody {
		t.Errorf("slim body = %q, want %q", m["body"], wantBody)
	}
	if len(m["items"].([]any)) > 4 {
		t.Errorf("slim mode: items should be capped at 3 + sentinel, got %d", len(m["items"].([]any)))
	}
}

func TestSlimMode_depthLimit(t *testing.T) {
	m := slimModeResult(t)
	nested := m["nested"].(map[string]any)
	if nested["a"] != "[depth limit reached]" {
		t.Errorf("slim mode: depth 2 should cut nested.a, got %v", nested["a"])
	}
}

func TestStripMarkup(t *testing.T) {
	cfg := &config.ProjectionConfig{StripMarkup: true}
	value := map[string]any{
		"body": "<p>Hello <strong>world</strong></p>",
	}

	result := projection.Apply(value, cfg, defaultLimits)
	m := result.Summary.(map[string]any)

	body := m["body"].(string)
	if body == "<p>Hello <strong>world</strong></p>" {
		t.Error("strip_markup should remove HTML tags")
	}
	if body != "Hello world" {
		t.Errorf("unexpected stripped body: %q", body)
	}
}

func autoStripResult(t *testing.T) (m map[string]any, longHTML, shortHTML string) {
	t.Helper()
	longHTML = "<p>" + strings.Repeat("word ", 120) + "</p>"
	shortHTML = "<p>Hello <strong>world</strong></p>"
	value := map[string]any{
		"body":        longHTML,
		"description": longHTML,
		"other":       longHTML,
		"body_short":  shortHTML,
	}
	m = projection.Apply(value, nil, defaultLimits).Summary.(map[string]any)
	return
}

func TestAutoStripContentFields_longContentFieldsStripped(t *testing.T) {
	m, longHTML, _ := autoStripResult(t)
	if m["body"] == longHTML {
		t.Error("body (long HTML) should be auto-stripped")
	}
	if m["description"] == longHTML {
		t.Error("description (long HTML) should be auto-stripped")
	}
}

func TestAutoStripContentFields_shortOrNonContentFieldsUnchanged(t *testing.T) {
	m, longHTML, shortHTML := autoStripResult(t)
	if m["other"] != longHTML {
		t.Errorf("other should not be auto-stripped, got %q", m["other"])
	}
	if m["body_short"] != shortHTML {
		t.Errorf("body_short (< threshold) should not be stripped, got %q", m["body_short"])
	}
}

func TestTruncateAtBoundary_utf8Safe(t *testing.T) {
	// "日本語" is 3 three-byte UTF-8 runes = 9 bytes.
	// With a byte limit that falls mid-rune, the result must be valid UTF-8.
	s := strings.Repeat("日本語", 200) // 600 runes, 1800 bytes
	cfg := &config.ProjectionConfig{StringLimits: map[string]int{"body": 100}}
	value := map[string]any{"body": s}
	result := projection.Apply(value, cfg, defaultLimits)
	m := result.Summary.(map[string]any)
	truncated := m["body"].(string)
	if !utf8.ValidString(truncated) {
		t.Errorf("truncated string is not valid UTF-8: %q", truncated[:min(20, len(truncated))])
	}
}

func TestArrayElementOmissionPath(t *testing.T) {
	long := strings.Repeat("x", 500)
	value := []any{long, long, "short"}

	result := projection.Apply(value, nil, &projection.Defaults{StringLimit: 100, DepthLimit: 5})

	if len(result.Omitted) != 2 {
		t.Fatalf("expected 2 omissions (one per long element), got %v", result.Omitted)
	}
	for i, o := range result.Omitted {
		want := "[" + string(rune('0'+i)) + "]"
		if o.Path != want {
			t.Errorf("omission[%d].Path = %q, want %q", i, o.Path, want)
		}
	}
}

func TestArrayElementOmissionPathInsideObject(t *testing.T) {
	long := strings.Repeat("x", 500)
	value := map[string]any{
		"lines": []any{long, long},
	}

	result := projection.Apply(value, nil, &projection.Defaults{StringLimit: 100, DepthLimit: 5})

	if len(result.Omitted) != 2 {
		t.Fatalf("expected 2 omissions, got %v", result.Omitted)
	}
	for i, o := range result.Omitted {
		want := ".lines[" + string(rune('0'+i)) + "]"
		if o.Path != want {
			t.Errorf("omission[%d].Path = %q, want %q", i, o.Path, want)
		}
	}
}

func TestOmitLimits(t *testing.T) {
	long := strings.Repeat("x", 500)
	cfg := &config.ProjectionConfig{
		OmitLimits: map[string]int{"patch": 100},
	}
	value := map[string]any{
		"patch": long,
		"title": long,
	}

	result := projection.Apply(value, cfg, &projection.Defaults{StringLimit: 1000, DepthLimit: 5})
	m := result.Summary.(map[string]any)

	if _, ok := m["patch"].(string); !ok {
		t.Fatal("patch should still be present as placeholder string")
	}
	if !strings.HasPrefix(m["patch"].(string), "<omitted:") {
		t.Errorf("patch should be replaced with omit placeholder, got %q", m["patch"])
	}
	if len(result.Omitted) != 1 || result.Omitted[0].Path != ".patch" {
		t.Errorf("expected one omission for .patch, got %v", result.Omitted)
	}
	if strings.HasPrefix(m["title"].(string), "<omitted:") {
		t.Errorf("title should not be omitted (no omit_limits entry), got %q", m["title"])
	}
}

func TestSlimKeepsFalseAndZeroValues(t *testing.T) {
	cfg := &config.ProjectionConfig{}
	value := map[string]any{
		"merged":   false,
		"draft":    false,
		"comments": float64(0),
		"stars":    float64(0),
		"title":    "fix issue",
	}
	result := projection.Apply(value, cfg, defaultLimits)
	m := result.Summary.(map[string]any)
	for _, key := range []string{"merged", "draft", "comments", "stars"} {
		if _, ok := m[key]; !ok {
			t.Errorf("key %q should be present in projected output, not dropped", key)
		}
	}
}
