//go:build integration

package integration_test

import (
	"strings"
	"testing"
)

func TestPermissions_defaultProtected(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	writeServerYAML(t, cfg, "svc", dir, "permissions:\n  default: protected\n")
	client := startServer(t, cfg)

	_, isErr := client.execToolAllowError("svc", "get_item", nil)
	if !isErr {
		t.Error("call on default-protected server should fail")
	}

	raw := client.mustCall("tools/call", map[string]any{
		"name":      "perm_call",
		"arguments": map[string]any{"server": "svc", "tool": "get_item", "args": map[string]any{}},
	})
	var r struct{ IsError bool `json:"isError"` }
	mustUnmarshal(t, raw, &r)
	if r.IsError {
		t.Error("perm_call on default-protected server should succeed")
	}
}

func TestPermissions_defaultHidden(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	writeServerYAML(t, cfg, "svc", dir, "permissions:\n  default: hidden\n")
	client := startServer(t, cfg)

	text := client.listTools("svc")
	if strings.Contains(text, "get_item") {
		t.Errorf("default-hidden server: get_item should not appear in list, got: %q", text)
	}

	_, isErr := client.execToolAllowError("svc", "get_item", nil)
	if !isErr {
		t.Error("call on hidden tool should fail")
	}
}

func TestPermissions_hiddenBeatsDefaultProtected(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"protected_tool": `{"id":1}`, "hidden_tool": `{"id":2}`})
	writeServerYAML(t, cfg, "svc", dir,
		"permissions:\n  default: protected\n  hidden:\n    - hidden_tool\n")
	client := startServer(t, cfg)

	text := client.listTools("svc")
	if !strings.Contains(text, "protected_tool") {
		t.Errorf("default-protected tool should appear in list, got: %q", text)
	}
	if strings.Contains(text, "hidden_tool") {
		t.Errorf("explicitly hidden tool should not appear in list, got: %q", text)
	}
}

func TestPermissions_listHiddenShowsHiddenTools(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"open_tool": `{"id":1}`, "secret_tool": `{"id":2}`})
	writeServerYAML(t, cfg, "svc", dir, "permissions:\n  hidden:\n    - secret_tool\n")
	client := startServer(t, cfg)

	// Normal list should not include secret_tool.
	if strings.Contains(client.listTools("svc"), "secret_tool") {
		t.Error("hidden tool should not appear in normal list")
	}

	// list(hidden:true) should include it.
	raw := client.mustCall("tools/call", map[string]any{
		"name":      "list",
		"arguments": map[string]any{"hidden": true},
	})
	text := toolCallText(t, raw)
	if !strings.Contains(text, "secret_tool") {
		t.Errorf("list(hidden:true) should include hidden tool, got: %q", text)
	}
	if !strings.Contains(text, "open_tool") {
		t.Errorf("list(hidden:true) should still include open tools, got: %q", text)
	}
}

func TestPermissions_disableListHidden(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"secret_tool": `{"id":2}`})
	writeServerYAML(t, cfg, "svc", dir, "permissions:\n  hidden:\n    - secret_tool\n")
	writeConfig(t, cfg, "disable_list_hidden: true\n")
	client := startServer(t, cfg)

	raw := client.mustCall("tools/call", map[string]any{
		"name":      "list",
		"arguments": map[string]any{"hidden": true},
	})
	var result struct {
		IsError bool                              `json:"isError"`
		Content []struct{ Text string `json:"text"` } `json:"content"`
	}
	mustUnmarshal(t, raw, &result)
	if !result.IsError {
		t.Error("list(hidden:true) should fail when disable_list_hidden:true")
	}
	if len(result.Content) > 0 && !strings.Contains(result.Content[0].Text, "disable_list_hidden") {
		t.Errorf("error should mention disable_list_hidden, got: %q", result.Content[0].Text)
	}
}

