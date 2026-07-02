package projection

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestMergeWithDefaultsNilConfigUsesDefaults(t *testing.T) {
	defaults := &Defaults{StringLimit: 1000, DepthLimit: 4,
		ContentFields: []string{"body", "summary"}, AutoStripThreshold: 500}
	got := mergeWithDefaults(nil, defaults)
	if got.defaultStringLimit != 1000 {
		t.Fatalf("defaultStringLimit = %d, want 1000", got.defaultStringLimit)
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

func TestDefaultsFrom(t *testing.T) {
	cfg := &config.Config{
		DefaultStringLimit: 100,
		DefaultDepthLimit:  5,
		ContentFields:      []string{"body", "text"},
		AutoStripThreshold: 200,
	}
	d := DefaultsFrom(cfg)
	if d.StringLimit != 100 {
		t.Errorf("StringLimit = %d, want 100", d.StringLimit)
	}
	if d.DepthLimit != 5 {
		t.Errorf("DepthLimit = %d, want 5", d.DepthLimit)
	}
	if len(d.ContentFields) != 2 || d.ContentFields[0] != "body" {
		t.Errorf("ContentFields = %v, want [body text]", d.ContentFields)
	}
	if d.AutoStripThreshold != 200 {
		t.Errorf("AutoStripThreshold = %d, want 200", d.AutoStripThreshold)
	}
}

func TestMergeWithDefaultsStripMarkupWithoutSlim(t *testing.T) {
	defaults := &Defaults{StringLimit: 1000, DepthLimit: 3}
	cfg := &config.ProjectionConfig{StripMarkup: true}

	got := mergeWithDefaults(cfg, defaults)

	if !got.stripContent {
		t.Fatal("expected stripContent override to enable stripping")
	}
	if got.defaultStringLimit != 1000 {
		t.Fatalf("defaultStringLimit = %d, want defaults preserved", got.defaultStringLimit)
	}
	if got.depthLimit != 3 {
		t.Fatalf("depthLimit = %d, want defaults preserved", got.depthLimit)
	}
}
