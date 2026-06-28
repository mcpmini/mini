package registry_test

import (
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/transport"
)

func TestToMap_includesOptionalFields(t *testing.T) {
	d := transport.ToolDefinition{
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
	d := transport.ToolDefinition{
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
