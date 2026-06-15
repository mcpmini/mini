package transport

import (
	"encoding/json"
	"testing"
)

func TestNormalizeID_float64_toInt64(t *testing.T) {
	if got := normalizeID(float64(42)); got != int64(42) {
		t.Errorf("expected int64(42), got %T(%v)", got, got)
	}
}

func TestNormalizeID_zero(t *testing.T) {
	if got := normalizeID(float64(0)); got != int64(0) {
		t.Errorf("expected int64(0), got %T(%v)", got, got)
	}
}

func TestNormalizeID_string_unchanged(t *testing.T) {
	if got := normalizeID("abc"); got != "abc" {
		t.Errorf("expected string passthrough, got %T(%v)", got, got)
	}
}

func TestNormalizeID_int64_unchanged(t *testing.T) {
	if got := normalizeID(int64(7)); got != int64(7) {
		t.Errorf("expected int64(7), got %T(%v)", got, got)
	}
}

func TestToToolDefs_mapsAllFields(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	in := []MCPTool{
		{Name: "read_file", Description: "reads a file", InputSchema: schema},
	}
	out := toToolDefs(in)
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
	if out[0].Name != "read_file" {
		t.Errorf("unexpected name: %s", out[0].Name)
	}
	if out[0].Description != "reads a file" {
		t.Errorf("unexpected description: %s", out[0].Description)
	}
	if string(out[0].InputSchema) != string(schema) {
		t.Errorf("schema not propagated: %s", out[0].InputSchema)
	}
}

func TestToToolDefs_empty(t *testing.T) {
	if out := toToolDefs(nil); len(out) != 0 {
		t.Errorf("expected empty, got %d", len(out))
	}
}

func TestToToolDefs_readOnlyHintPropagated(t *testing.T) {
	in := []MCPTool{
		{Name: "get_file", Annotations: json.RawMessage(`{"readOnlyHint":true}`)},
		{Name: "write_file"},
	}
	out := toToolDefs(in)
	if !out[0].ReadOnly {
		t.Error("readOnlyHint=true should set ReadOnly=true")
	}
	if out[1].ReadOnly {
		t.Error("tool without annotation should have ReadOnly=false")
	}
}

func TestToToolDefs_annotationsPassthrough(t *testing.T) {
	raw := json.RawMessage(`{"readOnlyHint":true,"destructiveHint":false,"fakeHint":true}`)
	in := []MCPTool{
		{Name: "get_file", Annotations: raw},
	}
	out := toToolDefs(in)
	if string(out[0].Annotations) != string(raw) {
		t.Errorf("annotations not preserved verbatim: got %s, want %s", out[0].Annotations, raw)
	}
}

func TestToToolDefs_absentAnnotationsPreserved(t *testing.T) {
	in := []MCPTool{
		{Name: "get_file", Annotations: json.RawMessage(`{"readOnlyHint":false}`)},
		{Name: "write_file"},
	}
	out := toToolDefs(in)
	if out[0].ReadOnly {
		t.Error("readOnlyHint=false should leave ReadOnly=false")
	}
	if len(out[1].Annotations) != 0 {
		t.Errorf("nil annotations must remain nil/empty, got: %s", out[1].Annotations)
	}
	if out[1].ReadOnly {
		t.Error("absent annotations should leave ReadOnly=false")
	}
}

func TestToToolDefs_multipleTools(t *testing.T) {
	in := []MCPTool{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}
	out := toToolDefs(in)
	if len(out) != 3 {
		t.Fatalf("expected 3, got %d", len(out))
	}
	for i, name := range []string{"a", "b", "c"} {
		if out[i].Name != name {
			t.Errorf("out[%d].Name = %s, want %s", i, out[i].Name, name)
		}
	}
}
