package server

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMergeArgs_overrideWins(t *testing.T) {
	defaults := map[string]any{"state": "open", "author": "me"}
	overrides := map[string]any{"state": "closed"}
	got := mergeArgs(defaults, overrides)
	if got["state"] != "closed" {
		t.Errorf("expected override state=closed, got %v", got["state"])
	}
	if got["author"] != "me" {
		t.Errorf("expected default author=me to be preserved, got %v", got["author"])
	}
}

func TestMergeArgs_defaultsPreserved(t *testing.T) {
	defaults := map[string]any{"limit": 10}
	overrides := map[string]any{}
	got := mergeArgs(defaults, overrides)
	if got["limit"] != 10 {
		t.Errorf("expected limit=10 from defaults, got %v", got["limit"])
	}
}

func TestMergeArgs_bothEmpty(t *testing.T) {
	got := mergeArgs(nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestMergeArgs_nilDefaults(t *testing.T) {
	overrides := map[string]any{"k": "v"}
	got := mergeArgs(nil, overrides)
	if got["k"] != "v" {
		t.Errorf("expected k=v, got %v", got)
	}
}

func TestUnmarshalOptional_emptyRaw(t *testing.T) {
	var v map[string]any
	if err := unmarshalOptional(nil, &v); err != nil {
		t.Errorf("unexpected error for nil raw: %v", err)
	}
	if v != nil {
		t.Errorf("expected nil map, got %v", v)
	}
}

func TestUnmarshalOptional_nullJSON(t *testing.T) {
	var v map[string]any
	if err := unmarshalOptional(json.RawMessage("null"), &v); err != nil {
		t.Errorf("unexpected error for null: %v", err)
	}
}

func TestUnmarshalOptional_validJSON(t *testing.T) {
	var v map[string]any
	if err := unmarshalOptional(json.RawMessage(`{"k":"v"}`), &v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v["k"] != "v" {
		t.Errorf("expected k=v, got %v", v)
	}
}

func TestUnmarshalOptional_invalidJSON(t *testing.T) {
	var v map[string]any
	if err := unmarshalOptional(json.RawMessage(`{bad json`), &v); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestNextBackoff_doublesBelow64s(t *testing.T) {
	cases := []struct {
		in, want time.Duration
	}{
		{1 * time.Second, 2 * time.Second},
		{2 * time.Second, 4 * time.Second},
		{4 * time.Second, 8 * time.Second},
		{32 * time.Second, 64 * time.Second},
	}
	for _, c := range cases {
		got := nextBackoff(c.in)
		if got != c.want {
			t.Errorf("nextBackoff(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNextBackoff_capsAt64s(t *testing.T) {
	got := nextBackoff(64 * time.Second)
	if got != 64*time.Second {
		t.Errorf("expected backoff to cap at 64s, got %v", got)
	}
}

func TestNextBackoff_alreadyAbove64s(t *testing.T) {
	got := nextBackoff(128 * time.Second)
	if got != 128*time.Second {
		t.Errorf("expected backoff unchanged above 64s, got %v", got)
	}
}
