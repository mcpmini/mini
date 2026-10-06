package agents

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

var testMiniEntry = map[string]any{"command": "/usr/local/bin/mini", "args": []string{"connect"}}

func TestEditJSONServers_removesImportedServersAndSetsMini(t *testing.T) {
	tests := []struct {
		name, config string
		remove       []string
		want         string
	}{
		{
			name:   "removal and addition",
			config: `{"mcpServers":{"github":{"url":"https://example.com/mcp"},"linear":{"url":"https://linear.example/mcp"}}}`,
			remove: []string{"github"},
			want:   "{\n  \"mcpServers\": {\n    \"linear\": {\n      \"url\": \"https://linear.example/mcp\"\n    },\n    \"mini\": {\n      \"args\": [\n        \"connect\"\n      ],\n      \"command\": \"/usr/local/bin/mini\"\n    }\n  }\n}\n",
		},
		{
			name:   "missing mcpServers is created and other settings are kept",
			config: `{"theme":"dark","numStartups":12}`,
			want:   "{\n  \"mcpServers\": {\n    \"mini\": {\n      \"args\": [\n        \"connect\"\n      ],\n      \"command\": \"/usr/local/bin/mini\"\n    }\n  },\n  \"numStartups\": 12,\n  \"theme\": \"dark\"\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EditJSONServers([]byte(tt.config), tt.remove, map[string]any{"mini": testMiniEntry})
			if err != nil || string(got) != tt.want {
				t.Errorf("EditJSONServers = %s, %v; want:\n%s", got, err, tt.want)
			}
		})
	}
}

func TestEditJSONServers_keepsValuesExactly(t *testing.T) {
	config := `{"userID":12345678901234567890,"ratio":0.1000,"mcpServers":{"search":{"url":"https://example.com/mcp?a=1&b=<2>"}}}`

	got, err := EditJSONServers([]byte(config), nil, map[string]any{"mini": testMiniEntry})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"userID": 12345678901234567890`, `"ratio": 0.1000`, `"url": "https://example.com/mcp?a=1&b=<2>"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("edited config lost %s:\n%s", want, got)
		}
	}
}

func TestEditJSONServers_refusesConfigsItCannotEditSafely(t *testing.T) {
	tests := []struct{ name, config, want string }{
		{"invalid JSON", `{"mcpServers":`, "parse agent config"},
		{"not an object", `["mcpServers"]`, "parse agent config"},
		{"null", `null`, "not a JSON object"},
		{"trailing data", `{"mcpServers":{}} {}`, "unexpected data"},
		{"stray closing brace", `{"mcpServers":{}} }`, "unexpected data"},
		{"mcpServers not an object", `{"mcpServers":["github"]}`, "mcpServers is not an object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EditJSONServers([]byte(tt.config), nil, map[string]any{"mini": testMiniEntry})
			if err == nil || !strings.Contains(err.Error(), tt.want) || got != nil {
				t.Fatalf("EditJSONServers = %s, %v; want no output and an error containing %q", got, err, tt.want)
			}
		})
	}
}

func TestEditJSONServers_preservesExistingMiniSettings(t *testing.T) {
	for _, entry := range []string{
		`{"command":"custom-mini","args":["connect","--standalone"],"env":{"TOKEN":"synthetic-token"},"enabled":false,"extra":12345678901234567890}`,
		`null`,
	} {
		t.Run(entry, func(t *testing.T) {
			original := []byte(`{"mcpServers":{"other":{"url":"https://example.com/mcp"},"mini":` + entry + `}}`)
			got, err := EditJSONServers(original, []string{"mini", "other"}, map[string]any{"mini": testMiniEntry})
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Servers map[string]json.RawMessage `json:"mcpServers"`
			}
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatal(err)
			}
			var actual, expected any
			if err := json.Unmarshal(decoded.Servers["mini"], &actual); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(entry), &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, expected) || len(decoded.Servers) != 1 {
				t.Fatalf("servers = %s; want only original mini %s", got, entry)
			}
			if strings.Contains(entry, "12345678901234567890") &&
				!strings.Contains(string(got), "12345678901234567890") {
				t.Fatalf("mini numeric value changed: %s", got)
			}
		})
	}
}
