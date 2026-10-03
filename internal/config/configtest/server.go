package configtest

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/testutil"
	"gopkg.in/yaml.v3"
)

func WriteServer(t testing.TB, dir string, server config.ServerConfig) {
	t.Helper()
	if !config.ValidServerName.MatchString(server.Name) {
		t.Fatalf("invalid server name %q", server.Name)
	}
	testutil.WriteFileBytes(t, config.ServerPath(dir, server.Name), writeYAML(t, server))
}

func writeYAML(t testing.TB, server config.ServerConfig) []byte {
	t.Helper()
	data, err := yaml.Marshal(server)
	if err != nil {
		t.Fatalf("marshal server %q: %v", server.Name, err)
	}
	return data
}
