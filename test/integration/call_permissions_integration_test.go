//go:build integration

package integration_test

import (
	"encoding/json"
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

func TestIntegrationCLICall_ProtectedTool_RequiresPermCall(t *testing.T) {
	cfg := callSetupWithPerms(t, map[string]string{"create_item": `{"id":1}`},
		&config.PermissionsConfig{Protected: []string{"create_item"}})
	_, stderr, code := runCLI(t, cfg, "call", "svc", "create_item")
	if code != 2 {
		t.Errorf("protected tool via call should exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "perm-call") {
		t.Errorf("expected 'perm-call' hint in stderr, got: %s", stderr)
	}
}

func TestIntegrationCLICall_PermCallBypassesProtection(t *testing.T) {
	cfg := callSetupWithPerms(t, map[string]string{"create_item": `{"id":1}`},
		&config.PermissionsConfig{Protected: []string{"create_item"}})
	stdout, _, code := runCLI(t, cfg, "perm-call", "svc", "create_item")
	if code != 0 {
		t.Fatalf("perm-call on protected tool should exit 0, got %d", code)
	}
	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v\nstdout: %s", err, stdout)
	}
	if env.Error != "" {
		t.Errorf("expected no error in envelope, got %q", env.Error)
	}
}

func TestIntegrationCLICall_HiddenTool_NotFound(t *testing.T) {
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

func TestIntegrationCLICall_DefaultProtected_RequiresPermCall(t *testing.T) {
	cfg := callSetupWithPerms(t, map[string]string{"any_tool": `{"id":1}`},
		&config.PermissionsConfig{Default: "protected"})
	_, stderr, code := runCLI(t, cfg, "call", "svc", "any_tool")
	if code != 2 {
		t.Errorf("default-protected tool via call should exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "perm-call") {
		t.Errorf("expected 'perm-call' hint in stderr, got: %s", stderr)
	}
	_, _, code = runCLI(t, cfg, "perm-call", "svc", "any_tool")
	if code != 0 {
		t.Errorf("perm-call on default-protected tool should exit 0, got %d", code)
	}
}

func TestIntegrationCLICall_DefaultHidden_NotFound(t *testing.T) {
	cfg := callSetupWithPerms(t, map[string]string{"any_tool": `{"id":1}`},
		&config.PermissionsConfig{Default: "hidden"})
	_, stderr, code := runCLI(t, cfg, "call", "svc", "any_tool")
	if code != 1 {
		t.Errorf("default-hidden tool should exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "tool not found") {
		t.Errorf("expected 'tool not found' in stderr, got: %s", stderr)
	}
}
