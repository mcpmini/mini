//go:build integration

package integration_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
)

func TestIntegrationPermissions_defaultHidden(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	writeFakeServer(t, cfg, fakeServerParams{
		ServerName: "svc",
		Fixtures:   dir,
		Permissions: &config.PermissionsConfig{
			Default: "hidden",
		},
	})
	client := startServer(t, cfg)

	text := client.listTools()
	if strings.Contains(text, "get_item") {
		t.Errorf("default-hidden server: get_item should not appear in list, got: %q", text)
	}

	_, isErr := client.nativeCallError("svc", "get_item")
	if !isErr {
		t.Error("call on hidden tool should fail")
	}
}

func TestIntegrationPermissions_hiddenBeatsDefaultProtected(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"protected_tool": `{"id":1}`, "hidden_tool": `{"id":2}`})
	writeFakeServer(t, cfg, fakeServerParams{
		ServerName:  "svc",
		Fixtures:    dir,
		Permissions: &config.PermissionsConfig{Default: "protected", Hidden: []string{"hidden_tool"}},
	})
	client := startServer(t, cfg)

	text := client.listTools()
	if !strings.Contains(text, "protected_tool") {
		t.Errorf("default-protected tool should appear in list, got: %q", text)
	}
	if strings.Contains(text, "hidden_tool") {
		t.Errorf("explicitly hidden tool should not appear in list, got: %q", text)
	}
}
