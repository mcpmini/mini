//go:build integration

package integration_test

import (
	"strings"
	"testing"
)

func TestIntegrationListTools_paginatesAcrossPages(t *testing.T) {
	fixtures := map[string]string{
		"tool_a": `{"result":"a"}`,
		"tool_b": `{"result":"b"}`,
		"tool_c": `{"result":"c"}`,
	}
	dir := mockFixtureDir(t, fixtures)
	cfg := t.TempDir()
	writeServerConfig(t, cfg, "svc", fakeServerYAML(dir, "--list-page-size", "2"))
	client := startServer(t, cfg)

	result := client.listTools("svc")
	for _, name := range []string{"tool_a", "tool_b", "tool_c"} {
		if !strings.Contains(result, name) {
			t.Errorf("tool %q missing from list output: %s", name, result)
		}
	}
}
