//go:build test

package transport

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestParseHeaderParametersAcceptsAnnotatedPrimitives(t *testing.T) {
	schema := `{"type":"object","properties":{
		"region":{"type":"string","x-mcp-header":"Region"},
		"dry":{"type":"boolean","x-mcp-header":"Dry-Run"},
		"target":{"type":"object","properties":{"id":{"type":"integer","x-mcp-header":"Target_Id"}}}}}`
	params, reason := parseHeaderParameters(json.RawMessage(schema))
	if reason != "" {
		t.Fatalf("valid schema rejected: %s", reason)
	}
	want := map[string][]string{"Region": {"region"}, "Dry-Run": {"dry"}, "Target_Id": {"target", "id"}}
	if len(params) != len(want) {
		t.Fatalf("header parameters = %+v, want %v", params, want)
	}
	for _, param := range params {
		if !slices.Equal(param.path, want[param.name]) {
			t.Errorf("header %s path = %v, want %v", param.name, param.path, want[param.name])
		}
	}
}

func TestParseHeaderParametersWithoutAnnotations(t *testing.T) {
	property := `{"type":"object","properties":{"x-mcp-header":{"type":"string"}}}`
	for _, raw := range []string{``, `null`, `{"type":"object"}`, property} {
		if params, reason := parseHeaderParameters(json.RawMessage(raw)); reason != "" || len(params) != 0 {
			t.Errorf("schema %q = %+v, %q; want no headers and no error", raw, params, reason)
		}
	}
}

func TestParseHeaderParametersRejectsInvalidSchemas(t *testing.T) {
	tests := map[string]string{
		"non-object schema": `[]`,
		"empty header name": `{"properties":{"p":{"type":"string","x-mcp-header":""}}}`,
		"non-string name":   `{"properties":{"p":{"type":"string","x-mcp-header":7}}}`,
		"nested duplicate": `{"properties":{"a":{"type":"string","x-mcp-header":"Id"},
			"b":{"type":"object","properties":{"c":{"type":"string","x-mcp-header":"ID"}}}}}`,
		"under $defs":        `{"$defs":{"p":{"type":"string","x-mcp-header":"P"}}}`,
		"under nested oneOf": `{"properties":{"p":{"type":"object","properties":{"q":{"oneOf":[{"x-mcp-header":"Q"}]}}}}}`,
		"under anyOf":        `{"properties":{"p":{"anyOf":[{"type":"string","x-mcp-header":"P"}]}}}`,
		"under items":        `{"properties":{"rows":{"type":"array","items":{"type":"string","x-mcp-header":"P"}}}}`,
		"on the root":        `{"type":"string","x-mcp-header":"P"}`,
		"number type":        `{"properties":{"p":{"type":"number","x-mcp-header":"P"}}}`,
		"missing type":       `{"properties":{"p":{"x-mcp-header":"P"}}}`,
		"type list":          `{"properties":{"p":{"type":["string"],"x-mcp-header":"P"}}}`,
		"space in name":      `{"properties":{"p":{"type":"string","x-mcp-header":"bad name"}}}`,
		"case-insensitive duplicate": `{"properties":{"a":{"type":"string","x-mcp-header":"X"},
			"b":{"type":"integer","x-mcp-header":"x"}}}`,
	}
	for name, schema := range tests {
		t.Run(name, func(t *testing.T) {
			if params, reason := parseHeaderParameters(json.RawMessage(schema)); reason == "" {
				t.Fatalf("schema accepted with headers %+v, want a rejection", params)
			}
		})
	}
}
