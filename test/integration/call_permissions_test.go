//go:build integration

package integration_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// callSetupWithPerms is like callSetup but appends extra YAML to the server config (e.g. permissions).
func callSetupWithPerms(t *testing.T, fixtures map[string]string, serverExtra string) string {
	t.Helper()
	cfg := t.TempDir()
	dir := mockFixtureDir(t, fixtures)
	writeServerYAML(t, cfg, "svc", dir, serverExtra)
	writeConfig(t, cfg, "inline_threshold: 500000\n")
	return cfg
}

func TestCLICall_ProtectedTool_RequiresPermCall(t *testing.T) {
	cfg := callSetupWithPerms(t, map[string]string{"create_item": `{"id":1}`},
		"permissions:\n  protected:\n    - create_item\n")
	_, stderr, code := runCLI(t, cfg, "call", "svc", "create_item")
	if code != 2 {
		t.Errorf("protected tool via call should exit 2, got %d", code)
	}
	if !strings.Contains(stderr, "perm-call") {
		t.Errorf("expected 'perm-call' hint in stderr, got: %s", stderr)
	}
}

func TestCLICall_PermCallBypassesProtection(t *testing.T) {
	cfg := callSetupWithPerms(t, map[string]string{"create_item": `{"id":1}`},
		"permissions:\n  protected:\n    - create_item\n")
	stdout, _, code := runCLI(t, cfg, "perm-call", "svc", "create_item")
	if code != 0 {
		t.Fatalf("perm-call on protected tool should exit 0, got %d", code)
	}
	var env struct{ Error string `json:"error"` }
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout not valid JSON: %v\nstdout: %s", err, stdout)
	}
	if env.Error != "" {
		t.Errorf("expected no error in envelope, got %q", env.Error)
	}
}

func TestCLICall_HiddenTool_NotFound(t *testing.T) {
	cfg := callSetupWithPerms(t, map[string]string{"secret_tool": `{"id":1}`},
		"permissions:\n  hidden:\n    - secret_tool\n")
	_, stderr, code := runCLI(t, cfg, "call", "svc", "secret_tool")
	if code != 1 {
		t.Errorf("hidden tool should exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "tool not found") {
		t.Errorf("expected 'tool not found' in stderr, got: %s", stderr)
	}
}

func TestCLICall_DefaultProtected_RequiresPermCall(t *testing.T) {
	cfg := callSetupWithPerms(t, map[string]string{"any_tool": `{"id":1}`},
		"permissions:\n  default: protected\n")
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

func TestCLICall_DefaultHidden_NotFound(t *testing.T) {
	cfg := callSetupWithPerms(t, map[string]string{"any_tool": `{"id":1}`},
		"permissions:\n  default: hidden\n")
	_, stderr, code := runCLI(t, cfg, "call", "svc", "any_tool")
	if code != 1 {
		t.Errorf("default-hidden tool should exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "tool not found") {
		t.Errorf("expected 'tool not found' in stderr, got: %s", stderr)
	}
}
