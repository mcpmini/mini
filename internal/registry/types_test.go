package registry_test

import (
	"encoding/json"
	"testing"

	"github.com/mcpmini/mini/internal/registry"
)

func TestSchemaFields_includesNewFields(t *testing.T) {
	e := &registry.ToolEntry{
		Description:  "test tool",
		InputSchema:  json.RawMessage(`{}`),
		Title:        json.RawMessage(`"My Tool"`),
		OutputSchema: json.RawMessage(`{"type":"string"}`),
		Meta:         json.RawMessage(`{"key":"val"}`),
		Icons:        json.RawMessage(`{"url":"http://example.com/icon.png"}`),
		Execution:    json.RawMessage(`{"timeout":30}`),
	}
	m := e.SchemaFields()
	for _, key := range []string{"title", "outputSchema", "_meta", "icons", "execution"} {
		if _, ok := m[key]; !ok {
			t.Errorf("SchemaFields() missing key %q", key)
		}
	}
}

func TestSchemaFields_excludesAbsentNewFields(t *testing.T) {
	e := &registry.ToolEntry{
		Description: "test tool",
		InputSchema: json.RawMessage(`{}`),
	}
	m := e.SchemaFields()
	for _, key := range []string{"title", "outputSchema", "_meta", "icons", "execution"} {
		if _, ok := m[key]; ok {
			t.Errorf("SchemaFields() should omit absent key %q", key)
		}
	}
}
