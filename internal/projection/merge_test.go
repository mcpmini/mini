package projection

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestMergeWithDefaultsNilConfigUsesDefaults(t *testing.T) {
	defaults := &Defaults{StringLimit: 1000, ArrayLimit: 7, DepthLimit: 4,
		ContentFields: []string{"body", "summary"}, AutoStripThreshold: 500}
	got := mergeWithDefaults(nil, defaults)
	if got.defaultStringLimit != 1000 {
		t.Fatalf("defaultStringLimit = %d, want 1000", got.defaultStringLimit)
	}
	if got.defaultArrayLimit != 7 {
		t.Fatalf("defaultArrayLimit = %d, want 7", got.defaultArrayLimit)
	}
	if got.depthLimit != 4 {
		t.Fatalf("depthLimit = %d, want 4", got.depthLimit)
	}
	if !got.contentFieldSet["body"] || !got.contentFieldSet["summary"] {
		t.Fatalf("contentFieldSet = %#v, want bundled content fields", got.contentFieldSet)
	}
	if got.autoStripThreshold != 500 {
		t.Fatalf("autoStripThreshold = %d, want 500", got.autoStripThreshold)
	}
}

func slimOverridesMerged(t *testing.T) *effectiveConfig {
	t.Helper()
	defaults := &Defaults{StringLimit: 1000, ArrayLimit: 7, DepthLimit: 4,
		ContentFields: []string{"body"}, AutoStripThreshold: 500}
	cfg := &config.ProjectionConfig{
		Mode: "slim", Include: []string{"items"}, ExcludeAlways: []string{"secret"},
		Passthrough: []string{"cursor"}, ArrayLimits: map[string]int{"items": 9},
		StringLimits: map[string]int{"body": 42}, DepthLimit: 5,
	}
	return mergeWithDefaults(cfg, defaults)
}

func TestMergeWithDefaultsSlimMode_setsSlimLimitsAndStrip(t *testing.T) {
	got := slimOverridesMerged(t)
	if got.defaultStringLimit != slimStringLimit {
		t.Fatalf("defaultStringLimit = %d, want %d", got.defaultStringLimit, slimStringLimit)
	}
	if got.defaultArrayLimit != slimArrayLimit {
		t.Fatalf("defaultArrayLimit = %d, want %d", got.defaultArrayLimit, slimArrayLimit)
	}
	if got.depthLimit != 5 {
		t.Fatalf("depthLimit = %d, want explicit override 5", got.depthLimit)
	}
	if !got.stripContent {
		t.Fatal("expected slim mode to enable stripContent")
	}
}

func TestMergeWithDefaultsSlimMode_respectsNamedLimits(t *testing.T) {
	got := slimOverridesMerged(t)
	if got.arrayLimitFor("items") != 9 {
		t.Fatalf("arrayLimitFor(items) = %d, want 9", got.arrayLimitFor("items"))
	}
	if got.arrayLimitFor("other") != slimArrayLimit {
		t.Fatalf("arrayLimitFor(other) = %d, want slim default %d", got.arrayLimitFor("other"), slimArrayLimit)
	}
	if got.stringLimitFor("body") != 42 {
		t.Fatalf("stringLimitFor(body) = %d, want 42", got.stringLimitFor("body"))
	}
	if got.stringLimitFor("title") != slimStringLimit {
		t.Fatalf("stringLimitFor(title) = %d, want slim default %d", got.stringLimitFor("title"), slimStringLimit)
	}
}

func TestMergeWithDefaultsSlimMode_preservesFilters(t *testing.T) {
	got := slimOverridesMerged(t)
	if len(got.include) != 1 || got.include[0] != "items" {
		t.Fatalf("include = %#v, want items", got.include)
	}
	if len(got.excludeAlways) != 1 || got.excludeAlways[0] != "secret" {
		t.Fatalf("excludeAlways = %#v, want secret", got.excludeAlways)
	}
	if len(got.passthrough) != 1 || got.passthrough[0] != "cursor" {
		t.Fatalf("passthrough = %#v, want cursor", got.passthrough)
	}
}

func TestMergeWithDefaultsStripMarkupWithoutSlim(t *testing.T) {
	defaults := &Defaults{StringLimit: 1000, ArrayLimit: 3, DepthLimit: 3}
	cfg := &config.ProjectionConfig{StripMarkup: true}

	got := mergeWithDefaults(cfg, defaults)

	if !got.stripContent {
		t.Fatal("expected stripContent override to enable stripping")
	}
	if got.defaultStringLimit != 1000 {
		t.Fatalf("defaultStringLimit = %d, want defaults preserved", got.defaultStringLimit)
	}
	if got.defaultArrayLimit != 3 {
		t.Fatalf("defaultArrayLimit = %d, want defaults preserved", got.defaultArrayLimit)
	}
	if got.depthLimit != 3 {
		t.Fatalf("depthLimit = %d, want defaults preserved", got.depthLimit)
	}
}
