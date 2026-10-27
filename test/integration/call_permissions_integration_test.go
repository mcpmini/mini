//go:build integration

package integration_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func callSetupWithPerms(t *testing.T, fixtures map[string]string, permissions *config.PermissionsConfig) string {
	t.Helper()
	cfg := t.TempDir()
	dir := mockFixtureDir(t, fixtures)
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir, Permissions: permissions})
	return cfg
}

func TestIntegrationCLICall_HiddenTool_NotFound(t *testing.T) {
	t.Parallel()
	cfg := callSetupWithPerms(t, map[string]string{"secret_tool": `{"id":1}`},
		&config.PermissionsConfig{Hidden: []string{"secret_tool"}})
	_, stderr, code := runCLI(t, cfg, "call", "svc", "secret_tool")
	if code != 1 {
		t.Errorf("hidden tool should exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "tool not found") {
		t.Errorf("expected 'tool not found' in stderr, got: %s", stderr)
	}
}
