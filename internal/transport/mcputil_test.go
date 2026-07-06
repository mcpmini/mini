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

func TestToMap_includesOptionalFields(t *testing.T) {
	d := ToolDefinition{
		Name:         "my_tool",
		Description:  "test tool",
		InputSchema:  json.RawMessage(`{}`),
		Title:        json.RawMessage(`"My Tool"`),
		OutputSchema: json.RawMessage(`{"type":"string"}`),
		Meta:         json.RawMessage(`{"key":"val"}`),
		Icons:        json.RawMessage(`{"url":"http://example.com/icon.png"}`),
		Execution:    json.RawMessage(`{"timeout":30}`),
	}
	m := d.ToMap()
	for _, key := range []string{"title", "outputSchema", "_meta", "icons", "execution"} {
		if _, ok := m[key]; !ok {
			t.Errorf("ToMap() missing key %q", key)
		}
	}
}

func TestToMap_excludesAbsentOptionalFields(t *testing.T) {
	d := ToolDefinition{
		Name:        "plain_tool",
		Description: "test tool",
		InputSchema: json.RawMessage(`{}`),
	}
	m := d.ToMap()
	for _, key := range []string{"title", "outputSchema", "_meta", "icons", "execution"} {
		if _, ok := m[key]; ok {
			t.Errorf("ToMap() should omit absent key %q", key)
		}
	}
}
