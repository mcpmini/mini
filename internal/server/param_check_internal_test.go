//go:build test

package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

func schemaEntry(schema string) *registry.ToolEntry {
	return &registry.ToolEntry{
		FullName: "srv.tool",
		Def:      transport.ToolDefinition{InputSchema: json.RawMessage(schema)},
	}
}

func TestCheckParamsAgainstSchema(t *testing.T) {
	cases := []struct {
		name      string
		schema    string
		params    map[string]any
		wantErr   bool
		wantTexts []string
	}{
		{
			name:    "valid params pass",
			schema:  `{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"}},"required":["owner","repo"]}`,
			params:  map[string]any{"owner": "a", "repo": "b"},
			wantErr: false,
		},
		{
			name:      "missing one required",
			schema:    `{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"}},"required":["owner","repo"]}`,
			params:    map[string]any{"owner": "a"},
			wantErr:   true,
			wantTexts: []string{`missing required "repo"`, `owner (string, required)`, `repo (string, required)`},
		},
		{
			name:      "missing multiple required",
			schema:    `{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"}},"required":["owner","repo"]}`,
			params:    map[string]any{},
			wantErr:   true,
			wantTexts: []string{`missing required "owner", "repo"`},
		},
		{
			name:      "extra key with additionalProperties false rejected",
			schema:    `{"type":"object","properties":{"owner":{"type":"string"}},"required":["owner"],"additionalProperties":false}`,
			params:    map[string]any{"owner": "a", "typo": "x"},
			wantErr:   true,
			wantTexts: []string{`unknown "typo"`},
		},
		{
			name:    "extra key with additionalProperties absent allowed",
			schema:  `{"type":"object","properties":{"owner":{"type":"string"}},"required":["owner"]}`,
			params:  map[string]any{"owner": "a", "extra": "x"},
			wantErr: false,
		},
		{
			name:    "extra key with sub-schema additionalProperties allowed",
			schema:  `{"type":"object","properties":{"owner":{"type":"string"}},"required":["owner"],"additionalProperties":{"type":"string"}}`,
			params:  map[string]any{"owner": "a", "extra": "x"},
			wantErr: false,
		},
		{
			name:      "sub-schema additionalProperties still enforces required",
			schema:    `{"type":"object","properties":{"owner":{"type":"string"}},"required":["owner"],"additionalProperties":{"type":"string"}}`,
			params:    map[string]any{},
			wantErr:   true,
			wantTexts: []string{`missing required "owner"`},
		},
		{
			name:    "empty schema passes",
			schema:  `{}`,
			params:  map[string]any{},
			wantErr: false,
		},
		{
			name:    "absent schema passes",
			schema:  ``,
			params:  map[string]any{},
			wantErr: false,
		},
		{
			name:    "unparseable schema passes",
			schema:  `not-json`,
			params:  map[string]any{},
			wantErr: false,
		},
		{
			name:    "no properties passes",
			schema:  `{"type":"object"}`,
			params:  map[string]any{"anything": "goes"},
			wantErr: false,
		},
		{
			name:      "type rendered from string",
			schema:    `{"type":"object","properties":{"n":{"type":"number"}},"required":["n"]}`,
			params:    map[string]any{},
			wantErr:   true,
			wantTexts: []string{"n (number, required)"},
		},
		{
			name:      "type rendered from array",
			schema:    `{"type":"object","properties":{"val":{"type":["string","null"]}},"required":["val"]}`,
			params:    map[string]any{},
			wantErr:   true,
			wantTexts: []string{"val (string|null, required)"},
		},
		{
			name:      "error text contains expected-params summary",
			schema:    `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"number"}},"required":["a","b"]}`,
			params:    map[string]any{},
			wantErr:   true,
			wantTexts: []string{"expected params:", "a (string, required)", "b (number, required)"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := schemaEntry(tc.schema)
			err := checkParamsAgainstSchema(e, tc.params)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range tc.wantTexts {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}
