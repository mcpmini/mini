//go:build integration

package integration_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestListTools_paginatesAcrossPages(t *testing.T) {
	fixtures := map[string]string{
		"tool_a": `{"result":"a"}`,
		"tool_b": `{"result":"b"}`,
		"tool_c": `{"result":"c"}`,
	}
	dir := mockFixtureDir(t, fixtures)
	cfg := t.TempDir()
	writeServerConfig(t, cfg, "svc", fmt.Sprintf(
		"name: svc\ncommand: %s\nargs:\n  - --fixtures\n  - %s\n  - --list-page-size\n  - \"2\"\n",
		fakemcpBin, dir,
	))
	client := startServer(t, cfg)

	result := client.listTools("svc")
	for _, name := range []string{"tool_a", "tool_b", "tool_c"} {
		if !strings.Contains(result, name) {
			t.Errorf("tool %q missing from list output: %s", name, result)
		}
	}
}
