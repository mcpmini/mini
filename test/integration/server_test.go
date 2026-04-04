//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServer_initialize(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", filepath.Join(fixturesDir, "github"))
	raw := startServer(t, cfg).mustCall("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	var result struct {
		ServerInfo struct{ Name string `json:"name"` } `json:"serverInfo"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.ServerInfo.Name != "mini" {
		t.Errorf("expected serverInfo.name=mini, got %q", result.ServerInfo.Name)
	}
}

func TestServer_toolsList(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", filepath.Join(fixturesDir, "github"))
	raw := startServer(t, cfg).mustCall("tools/list", nil)
	var result struct {
		Tools []struct{ Name string `json:"name"` } `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"list", "call", "perm_call", "config"} {
		if !names[want] {
			t.Errorf("expected tool %q in tools/list, got: %v", want, names)
		}
	}
}

func TestServer_listUpstreamTools(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", filepath.Join(fixturesDir, "github"))
	text := startServer(t, cfg).listTools("github")
	for _, want := range []string{"list_pull_requests", "list_issues", "get_file_contents", "search_code"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in list output, got: %q", want, text)
		}
	}
}

func TestServer_execReturnsResponse(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", filepath.Join(fixturesDir, "github"))
	text := startServer(t, cfg).execTool("github", "list_pull_requests", nil)
	if !strings.Contains(text, `"ok"`) {
		t.Errorf("expected ok envelope in response, got: %q", text[:min(200, len(text))])
	}
}

func TestServer_execWithProjection(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", filepath.Join(fixturesDir, "github"))
	writeProjection(t, cfg, "github", "list_pull_requests:\n  include: [number, title]\n")

	rawFixture, err := os.ReadFile(filepath.Join(fixturesDir, "github", "list_pull_requests.json"))
	if err != nil {
		t.Fatal(err)
	}
	projected := startServer(t, cfg).execTool("github", "list_pull_requests", nil)
	if len(projected) >= len(rawFixture) {
		t.Errorf("projected (%d bytes) should be smaller than raw (%d bytes)", len(projected), len(rawFixture))
	}
	if !strings.Contains(projected, "number") {
		t.Errorf("projected response should contain 'number', got: %q", projected[:min(200, len(projected))])
	}
}

func TestServer_execUnknownTool(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", filepath.Join(fixturesDir, "github"))
	raw := startServer(t, cfg).mustCall("tools/call", map[string]any{
		"name":      "call",
		"arguments": map[string]any{"server": "github", "tool": "nonexistent_tool", "args": map[string]any{}},
	})
	var result struct{ IsError bool `json:"isError"` }
	json.Unmarshal(raw, &result)
	if !result.IsError {
		t.Error("expected isError=true for unknown tool")
	}
}

func TestServer_execUnknownServer(t *testing.T) {
	cfg := t.TempDir()
	raw := startServer(t, cfg).mustCall("tools/call", map[string]any{
		"name":      "call",
		"arguments": map[string]any{"server": "doesnotexist", "tool": "list_pull_requests", "args": map[string]any{}},
	})
	var result struct{ IsError bool `json:"isError"` }
	json.Unmarshal(raw, &result)
	if !result.IsError {
		t.Error("expected isError=true for unknown server")
	}
}

func writeGitHubServerYAML(t *testing.T, cfg, permKey, tool string) {
	t.Helper()
	serversDir := filepath.Join(cfg, "servers")
	os.MkdirAll(serversDir, 0700) //nolint:errcheck
	yaml := "name: github\ncommand: " + fakemcpBin + "\nargs:\n  - --fixtures\n  - " +
		filepath.Join(fixturesDir, "github") + "\npermissions:\n  " + permKey + ":\n    - " + tool + "\n"
	os.WriteFile(filepath.Join(serversDir, "github.yaml"), []byte(yaml), 0600) //nolint:errcheck
}

func execGitHubToolIsError(t *testing.T, client *mcpClient, execName, tool string) bool {
	t.Helper()
	raw := client.mustCall("tools/call", map[string]any{
		"name":      execName,
		"arguments": map[string]any{"server": "github", "tool": tool, "args": map[string]any{}},
	})
	var r struct{ IsError bool `json:"isError"` }
	json.Unmarshal(raw, &r) //nolint:errcheck
	return r.IsError
}

func TestServer_protectedToolRequiresExecProtected(t *testing.T) {
	cfg := t.TempDir()
	writeGitHubServerYAML(t, cfg, "protected", "list_pull_requests")
	client := startServer(t, cfg)
	if !execGitHubToolIsError(t, client, "call", "list_pull_requests") {
		t.Error("expected call to fail for protected tool")
	}
	if execGitHubToolIsError(t, client, "perm_call", "list_pull_requests") {
		t.Error("expected perm_call to succeed for protected tool")
	}
}

func TestServer_hiddenToolNotListed(t *testing.T) {
	cfg := t.TempDir()
	serversDir := filepath.Join(cfg, "servers")
	os.MkdirAll(serversDir, 0700)
	yaml := "name: github\ncommand: " + fakemcpBin + "\nargs:\n  - --fixtures\n  - " +
		filepath.Join(fixturesDir, "github") + "\npermissions:\n  hidden:\n    - search_code\n"
	os.WriteFile(filepath.Join(serversDir, "github.yaml"), []byte(yaml), 0600)
	text := startServer(t, cfg).listTools("github")
	if strings.Contains(text, "search_code") {
		t.Errorf("hidden tool 'search_code' should not appear in list output")
	}
	if !strings.Contains(text, "list_pull_requests") {
		t.Errorf("non-hidden tool 'list_pull_requests' should appear in list output")
	}
}

func TestServer_configureProjectionOverride(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", filepath.Join(fixturesDir, "github"))
	client := startServer(t, cfg)
	client.setProjection("github", "list_pull_requests", map[string]any{"include": []string{"number"}, "depth_limit": 1}, true)

	rawFixture, _ := os.ReadFile(filepath.Join(fixturesDir, "github", "list_pull_requests.json"))
	projected := client.execTool("github", "list_pull_requests", nil)
	if len(projected) >= len(rawFixture) {
		t.Errorf("configure-projected response (%d) should be smaller than raw (%d)", len(projected), len(rawFixture))
	}
}

func TestServer_listAllTools(t *testing.T) {
	cfg := t.TempDir()
	serversDir := filepath.Join(cfg, "servers")
	os.MkdirAll(serversDir, 0700)
	for _, srv := range []string{"alpha", "beta"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "do_"+srv+".json"), []byte(`{"ok":true}`), 0644)
		yaml := "name: " + srv + "\ncommand: " + fakemcpBin + "\nargs:\n  - --fixtures\n  - " + dir + "\n"
		os.WriteFile(filepath.Join(serversDir, srv+".yaml"), []byte(yaml), 0600)
	}
	raw := startServer(t, cfg).mustCall("tools/call", map[string]any{
		"name":      "list",
		"arguments": map[string]any{},
	})
	text := toolCallText(t, raw)
	for _, want := range []string{"do_alpha", "do_beta"} {
		if !strings.Contains(text, want) {
			t.Errorf("list() with no server arg should include %q, got: %q", want, text)
		}
	}
}

func TestServer_multipleUpstreams(t *testing.T) {
	cfg := t.TempDir()
	serversDir := filepath.Join(cfg, "servers")
	os.MkdirAll(serversDir, 0700)

	for _, srv := range []string{"alpha", "beta"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "do_thing.json"), []byte(`{"ok":true}`), 0644)
		yaml := "name: " + srv + "\ncommand: " + fakemcpBin + "\nargs:\n  - --fixtures\n  - " + dir + "\n"
		os.WriteFile(filepath.Join(serversDir, srv+".yaml"), []byte(yaml), 0600)
	}
	client := startServer(t, cfg)
	for _, srv := range []string{"alpha", "beta"} {
		if !strings.Contains(client.listTools(srv), "do_thing") {
			t.Errorf("%s: expected do_thing in list", srv)
		}
	}
}

