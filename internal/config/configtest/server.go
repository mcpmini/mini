package configtest

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
)

func WriteServer(t testing.TB, dir string, server config.ServerConfig) {
	t.Helper()
	if !config.ValidServerName.MatchString(server.Name) {
		t.Fatalf("invalid server name %q", server.Name)
	}
	testutil.WriteFileBytes(t, config.ServerPath(dir, server.Name), writeYAML(t, server))
}

func writeYAML(t testing.TB, value any) []byte {
	t.Helper()
	data, err := yaml.Marshal(value)
	if err != nil {
		t.Fatalf("marshal YAML: %v", err)
	}
	return data
}
