package catalog

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	entries, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 38 {
		t.Errorf("entries = %d, want 38", len(entries))
	}
}

func TestParseRejectsInvalidEntries(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"bad name", "schema_version: 1\nentries:\n  - name: bad name\n    url: https://example.com\n    description: test\n    category: test\n", "bad name"},
		{"http url", "schema_version: 1\nentries:\n  - name: example\n    url: http://example.com\n    description: test\n    category: test\n", "example"},
		{"missing description", "schema_version: 1\nentries:\n  - name: example\n    url: https://example.com\n    category: test\n", "example"},
		{"blank category", "schema_version: 1\nentries:\n  - name: example\n    url: https://example.com\n    description: test\n    category: ' '\n", "example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parse error = %v, want entry %q", err, tt.want)
			}
		})
	}
}
