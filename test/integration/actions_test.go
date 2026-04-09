//go:build integration

package integration_test

import (
	"strings"
	"testing"
)

func actionServer(t *testing.T, fixtures map[string]string, actionYAML, actionName string) *mcpClient {
	t.Helper()
	cfg := t.TempDir()
	dir := mockFixtureDir(t, fixtures)
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")
	writeAction(t, cfg, actionYAML, actionName)
	return startServer(t, cfg)
}

func TestActions_defaultArgsMergedWithCallArgs(t *testing.T) {
	client := actionServer(t,
		map[string]string{"get_item": `{"id":42,"name":"fetched"}`},
		"name: myfetch\ndescription: Fetch\nserver: svc\ntool: get_item\ndefault_args:\n  id: 42\n  extra: default\n",
		"myfetch")

	e := client.execEnvelope("svc", "myfetch", map[string]any{"id": 99})
	if e.Error != "" {
		t.Errorf("action with call-time args overriding default should succeed, got: %+v", e)
	}
}

func TestActions_protectedActionRequiresExecProtected(t *testing.T) {
	client := actionServer(t,
		map[string]string{"get_item": `{"id":1}`},
		"name: protected_fetch\ndescription: Protected\nserver: svc\ntool: get_item\npermission: protected\n",
		"protected_fetch")

	_, isErr := client.execToolAllowError("svc", "protected_fetch", nil)
	if !isErr {
		t.Error("call on protected action should fail")
	}
	_, isErr = client.execProtectedAllowError("svc", "protected_fetch", nil)
	if isErr {
		t.Error("perm_call on protected action should succeed")
	}
}

func TestActions_badServerReference(t *testing.T) {
	client := actionServer(t,
		map[string]string{"get_item": `{"id":1}`},
		"name: broken\ndescription: Bad server\nserver: nonexistent\ntool: get_item\n",
		"broken")

	_, isErr := client.execToolAllowError("nonexistent", "broken", nil)
	if !isErr {
		t.Error("action referencing nonexistent server should return error")
	}
}

func TestActions_execAction(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":42,"name":"fetched"}`})
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")
	writeAction(t, cfg, "name: myfetch\ndescription: Fetch item 42\nserver: svc\ntool: get_item\ndefault_args:\n  id: 42\n", "myfetch")

	client := startServer(t, cfg)

	if !strings.Contains(client.listTools("svc"), "myfetch") {
		t.Error("expected action 'myfetch' to appear in list")
	}

	e := client.execEnvelope("svc", "myfetch", nil)
	if e.Error != "" {
		t.Errorf("expected ok=true from action call, got: %+v", e)
	}
}
