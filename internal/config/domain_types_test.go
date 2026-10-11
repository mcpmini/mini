package config_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
)

func TestNamedDomains_preserveUnknownYAMLValues(t *testing.T) {
	raw := "transport: future-transport\nsession_mode: future-session\nauth:\n  type: future-auth\npermissions:\n  default: future-permission\n  protected: []\n  hidden: []\n"
	var server config.ServerConfig
	if err := yaml.Unmarshal([]byte(raw), &server); err != nil {
		t.Fatal(err)
	}
	if server.IsHTTPTransport() || server.Permissions.LevelFor("tool") != config.PermOpen {
		t.Fatalf("unknown values changed fallback behavior: %+v", server)
	}
	encoded, err := yaml.Marshal(server)
	if err != nil {
		t.Fatal(err)
	}
	before, after := domainYAMLMap(t, []byte(raw)), domainYAMLMap(t, encoded)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("YAML changed: before=%v after=%v", before, after)
	}
}

func TestNamedResponseFormat_preservesJSONAndOmission(t *testing.T) {
	for _, raw := range []string{`{}`, `{"format":"json"}`, `{"format":"toon"}`, `{"format":"future-format"}`} {
		var projection config.ProjectionConfig
		if err := json.Unmarshal([]byte(raw), &projection); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(projection)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != raw {
			t.Errorf("JSON changed: got %s want %s", encoded, raw)
		}
	}
}

func domainYAMLMap(t *testing.T, encoded []byte) map[string]any {
	t.Helper()
	var fields map[string]any
	if err := yaml.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}
