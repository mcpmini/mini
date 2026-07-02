package projection_test

import (
	"slices"
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
	cfg := &config.ProjectionConfig{IncludeOnly: []string{"status", "branch"}}
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

func TestExclude(t *testing.T) {
	cfg := &config.ProjectionConfig{Exclude: []string{"env", "pipeline.configuration"}}
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
	t.Run("per-projection limit produces truncation metadata", func(t *testing.T) {
		value := map[string]any{"steps": []any{"a", "b", "c", "d", "e"}}
		cfg := &config.ProjectionConfig{ArrayLimits: map[string]int{"steps": 2}}
		result := projection.Apply(value, cfg, &projection.Defaults{StringLimit: 1000, DepthLimit: 5})
		m := result.Summary.(map[string]any)
		steps := m["steps"].([]any)
		if len(steps) != 2 {
			t.Errorf("expected 2 items (no sentinel), got %d", len(steps))
		}
		if len(result.Truncated) != 1 || result.Truncated[0].JQPath != ".steps" || result.Truncated[0].Items != 3 {
			t.Errorf("expected truncation metadata {.steps items:3}, got %v", result.Truncated)
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
	if len(result.Truncated) != 1 || result.Truncated[0].Chars != 1900 {
		t.Errorf("truncated = %v, want [{.body chars:1900}]", result.Truncated)
	}
}

func TestStringTruncation_CJKUsesCharCount(t *testing.T) {
	// 700 CJK chars = 2100 bytes. A 2000-char limit should keep all 700 chars.
	cjk := strings.Repeat("中", 700)
	value := map[string]any{"body": cjk}
	result := projection.Apply(value, nil, &projection.Defaults{StringLimit: 2000, DepthLimit: 5})
	m := result.Summary.(map[string]any)
	if m["body"] != cjk {
		t.Errorf("expected 700 CJK chars preserved, got %d chars", utf8.RuneCountInString(m["body"].(string)))
	}
	if len(result.Truncated) != 0 {
		t.Errorf("expected no truncation for 700 chars under 2000-char limit, got %v", result.Truncated)
	}
}

func TestStringTruncation_CJKTruncatesAtRuneCount(t *testing.T) {
	// 1000 CJK chars = 3000 bytes. A 500-char limit should keep ~500 chars, not ~166.
	cjk := strings.Repeat("中", 1000)
	value := map[string]any{"body": cjk}
	result := projection.Apply(value, nil, &projection.Defaults{StringLimit: 500, DepthLimit: 5})
	m := result.Summary.(map[string]any)
	got := utf8.RuneCountInString(m["body"].(string))
	if got > 500 {
		t.Errorf("expected at most 500 CJK chars, got %d", got)
	}
	if got < 450 {
		t.Errorf("expected at least 450 CJK chars (near 500-char limit), got %d", got)
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

func TestExcludedKeys_sortedDeterministic(t *testing.T) {
	cfg := &config.ProjectionConfig{Exclude: []string{"zebra", "apple", "mango"}}
	value := map[string]any{"zebra": 1, "apple": 2, "mango": 3, "keep": 4}
	result := projection.Apply(value, cfg, defaultLimits)
	want := []string{".apple", ".mango", ".zebra"}
	for i, k := range result.ExcludedKeys {
		if k != want[i] {
			t.Errorf("ExcludedKeys = %v, want %v (sorted)", result.ExcludedKeys, want)
			break
		}
	}
}

func TestExcludedKeys(t *testing.T) {
	cfg := &config.ProjectionConfig{IncludeOnly: []string{"status"}}
	value := map[string]any{
		"status": "ok",
		"env":    "secret",
		"body":   "long text",
	}

	result := projection.Apply(value, cfg, defaultLimits)
	want := []string{".body", ".env"}
	if !slices.Equal(result.ExcludedKeys, want) {
		t.Errorf("ExcludedKeys = %v, want %v", result.ExcludedKeys, want)
	}
}

func TestPassthroughFields(t *testing.T) {
	cfg := &config.ProjectionConfig{
		IncludeOnly:  []string{"status"},
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
	cfg := &config.ProjectionConfig{IncludeOnly: []string{"issues", "totalCount"}}
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
	cfg := &config.ProjectionConfig{IncludeOnly: []string{"issues"}}
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

func namedArrayLimitsApply(t *testing.T) projection.Result {
	t.Helper()
	cfg := &config.ProjectionConfig{ArrayLimits: map[string]int{"issues": 5}}
	value := map[string]any{
		"issues":     []any{1, 2, 3, 4, 5, 6, 7},
		"other_list": []any{"a", "b", "c", "d", "e"},
	}
	limits := &projection.Defaults{StringLimit: 1000, DepthLimit: 5,
		ContentFields: []string{}, AutoStripThreshold: 0}
	return projection.Apply(value, cfg, limits)
}

func TestNamedArrayLimit_namedLimitApplied(t *testing.T) {
	result := namedArrayLimitsApply(t)
	m := result.Summary.(map[string]any)
	issues := m["issues"].([]any)
	if len(issues) != 5 {
		t.Errorf("expected 5 items (no sentinel), got %d", len(issues))
	}
	if len(result.Truncated) != 1 || result.Truncated[0].JQPath != ".issues" || result.Truncated[0].Items != 2 {
		t.Errorf("expected truncation metadata {.issues items:2}, got %v", result.Truncated)
	}
}

func TestNamedArrayLimit_unlimitedForOtherFields(t *testing.T) {
	result := namedArrayLimitsApply(t)
	m := result.Summary.(map[string]any)
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
	if len(result.Truncated) != 1 || result.Truncated[0].JQPath != ".body" || result.Truncated[0].Chars != 20 {
		t.Errorf("truncated = %v, want [{.body chars:20}]", result.Truncated)
	}
	// title has no named limit — should pass through untruncated
	if m["title"].(string) != long {
		t.Errorf("title should not be truncated")
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

func TestTruncateAtBoundary_exactLimitNotTruncated(t *testing.T) {
	s := strings.Repeat("x", 100)
	cfg := &config.ProjectionConfig{StringLimits: map[string]int{"body": 100}}
	result := projection.Apply(map[string]any{"body": s}, cfg, defaultLimits)
	m := result.Summary.(map[string]any)
	if got := m["body"].(string); got != s {
		t.Errorf("string at exact limit should be unchanged, got len=%d", len(got))
	}
	if len(result.Truncated) != 0 {
		t.Errorf("string at exact limit should produce no Truncated entry, got %v", result.Truncated)
	}
}

func TestArrayElementOmissionPath(t *testing.T) {
	long := strings.Repeat("x", 500)
	value := []any{long, long, "short"}

	result := projection.Apply(value, nil, &projection.Defaults{StringLimit: 100, DepthLimit: 5})

	if len(result.Truncated) != 2 {
		t.Fatalf("expected 2 omissions (one per long element), got %v", result.Truncated)
	}
	for i, o := range result.Truncated {
		want := ".[" + string(rune('0'+i)) + "]"
		if o.JQPath != want {
			t.Errorf("truncation[%d].JQPath = %q, want %q", i, o.JQPath, want)
		}
	}
}

func TestArrayElementOmissionPathInsideObject(t *testing.T) {
	long := strings.Repeat("x", 500)
	value := map[string]any{
		"lines": []any{long, long},
	}

	result := projection.Apply(value, nil, &projection.Defaults{StringLimit: 100, DepthLimit: 5})

	if len(result.Truncated) != 2 {
		t.Fatalf("expected 2 omissions, got %v", result.Truncated)
	}
	for i, o := range result.Truncated {
		want := ".lines[" + string(rune('0'+i)) + "]"
		if o.JQPath != want {
			t.Errorf("truncation[%d].JQPath = %q, want %q", i, o.JQPath, want)
		}
	}
}

func TestElidedPathBracketNotationForNonIdentifierKey(t *testing.T) {
	value := map[string]any{
		"body text": strings.Repeat("x", 500),
		"normal":    strings.Repeat("x", 500),
	}
	result := projection.Apply(value, nil, &projection.Defaults{StringLimit: 100, DepthLimit: 5})

	paths := make(map[string]bool)
	for _, o := range result.Truncated {
		paths[o.JQPath] = true
	}
	if !paths[`.["body text"]`] {
		t.Errorf("expected bracket notation for key with space, got paths %v", result.Truncated)
	}
	if !paths[".normal"] {
		t.Errorf("expected dot notation for identifier-safe key, got paths %v", result.Truncated)
	}
}

func TestDefaultArrayLimit(t *testing.T) {
	t.Run("default truncates root-level array", func(t *testing.T) {
		cfg := &config.ProjectionConfig{ArrayLimits: map[string]int{"default": 2}}
		value := []any{1, 2, 3, 4, 5}
		result := projection.Apply(value, cfg, &projection.Defaults{StringLimit: 1000, DepthLimit: 5})
		arr := result.Summary.([]any)
		if len(arr) != 2 {
			t.Errorf("expected 2 items, got %d", len(arr))
		}
		if len(result.Truncated) != 1 || result.Truncated[0].Items != 3 || result.Truncated[0].JQPath != "." {
			t.Errorf("expected truncation {path:\".\", items:3}, got %v", result.Truncated)
		}
	})

	t.Run("default applies to unnamed arrays, named limit wins for named field", func(t *testing.T) {
		cfg := &config.ProjectionConfig{ArrayLimits: map[string]int{"default": 2, "labels": 5}}
		value := map[string]any{
			"labels": []any{1, 2, 3, 4, 5, 6, 7, 8},
			"other":  []any{"a", "b", "c", "d", "e"},
		}
		result := projection.Apply(value, cfg, &projection.Defaults{StringLimit: 1000, DepthLimit: 5})
		m := result.Summary.(map[string]any)
		if len(m["labels"].([]any)) != 5 {
			t.Errorf("labels: expected 5 items (named limit), got %d", len(m["labels"].([]any)))
		}
		if len(m["other"].([]any)) != 2 {
			t.Errorf("other: expected 2 items (default limit), got %d", len(m["other"].([]any)))
		}
		if len(result.Truncated) != 2 {
			t.Fatalf("expected 2 truncation entries, got %v", result.Truncated)
		}
		if result.Truncated[0].JQPath != ".labels" || result.Truncated[0].Items != 3 {
			t.Errorf("truncated[0]: expected {.labels items:3}, got %v", result.Truncated[0])
		}
		if result.Truncated[1].JQPath != ".other" || result.Truncated[1].Items != 3 {
			t.Errorf("truncated[1]: expected {.other items:3}, got %v", result.Truncated[1])
		}
	})
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

func TestTopLevelString_longTruncatesWithDotPath(t *testing.T) {
	long := strings.Repeat("word ", 300) // 1500 runes, over defaultLimits.StringLimit=1000
	result := projection.Apply(long, nil, defaultLimits)

	s, ok := result.Summary.(string)
	if !ok {
		t.Fatalf("expected string summary, got %T", result.Summary)
	}
	if utf8.RuneCountInString(s) >= 1500 {
		t.Error("expected truncation, summary is still full length")
	}
	if len(result.Truncated) != 1 {
		t.Fatalf("expected 1 truncation, got %d", len(result.Truncated))
	}
	if result.Truncated[0].JQPath != "." {
		t.Errorf("JQPath = %q, want %q", result.Truncated[0].JQPath, ".")
	}
	if result.Truncated[0].Chars <= 0 {
		t.Errorf("Chars = %d, expected > 0", result.Truncated[0].Chars)
	}
}

func TestTopLevelString_shortPassesThrough(t *testing.T) {
	short := "hello world"
	result := projection.Apply(short, nil, defaultLimits)

	s, ok := result.Summary.(string)
	if !ok {
		t.Fatalf("expected string summary, got %T", result.Summary)
	}
	if s != short {
		t.Errorf("short string changed: got %q, want %q", s, short)
	}
	if len(result.Truncated) != 0 {
		t.Errorf("expected no truncations for short string, got %v", result.Truncated)
	}
}

func TestTopLevelScalar_passesThrough(t *testing.T) {
	for _, v := range []any{float64(42), true, false, nil} {
		result := projection.Apply(v, nil, defaultLimits)
		if result.Summary != v {
			t.Errorf("scalar %v changed to %v", v, result.Summary)
		}
		if len(result.Truncated) != 0 {
			t.Errorf("scalar %v produced unexpected truncations", v)
		}
	}
}

func TestTopLevelString_markupTreatedAsPlainString(t *testing.T) {
	// Mini does not parse or strip markup in top-level strings — only in string
	// fields within objects (where the HTML/MD stripping logic runs). This test
	// documents that behavior as intentional: truncation is by rune count only.
	xml := strings.Repeat("<item>value</item>", 200) // 3600 chars, over defaultLimits.StringLimit=1000
	result := projection.Apply(xml, nil, defaultLimits)

	s, ok := result.Summary.(string)
	if !ok {
		t.Fatalf("expected string summary, got %T", result.Summary)
	}
	// String must be truncated but markup must not be stripped.
	if utf8.RuneCountInString(s) >= 3600 {
		t.Error("expected top-level XML string to be truncated")
	}
	if !strings.Contains(s, "<item>") {
		t.Error("markup should be preserved in top-level string (no HTML stripping at root level)")
	}
	if len(result.Truncated) == 0 {
		t.Error("expected truncation metadata for oversized top-level string")
	}
}
