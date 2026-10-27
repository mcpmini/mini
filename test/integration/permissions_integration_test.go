//go:build integration

package integration_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestIntegrationPermissions_hiddenToolIsNeitherListedNorCallable(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"visible_tool": `{"id":1}`, "hidden_tool": `{"id":2}`})
	writeFakeServer(t, cfg, fakeServerParams{
		ServerName:  "svc",
		Fixtures:    dir,
		Permissions: &config.PermissionsConfig{Hidden: []string{"hidden_tool"}},
	})
	client := startServer(t, cfg)

	text := client.listTools()
	if !strings.Contains(text, "visible_tool") || strings.Contains(text, "hidden_tool") {
		t.Errorf("tools/list should show visible_tool and not hidden_tool, got: %q", text)
	}
	if _, isErr := client.nativeCallError("svc", "hidden_tool"); !isErr {
		t.Error("call on hidden tool should fail")
	}
}
