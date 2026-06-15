//go:build integration

package integration_test

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
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
	if !strings.Contains(text, `"data"`) {
		t.Errorf("expected data envelope in response, got: %q", text[:min(200, len(text))])
	}
}

func TestServer_execWithProjection(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", filepath.Join(fixturesDir, "github"))
	writeProjection(t, cfg, "github", "list_pull_requests:\n  include: [number, title]\n")
	writeConfig(t, cfg, "inline_threshold: 50000\n")

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

// startProxyServer starts mini in proxy mode and returns an mcpClient.
func startProxyServer(t *testing.T, configDir string) *mcpClient {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", configDir, "proxy", "--log-level", "error")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	})
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4<<20), 4<<20)
	c := &mcpClient{stdin: stdin, done: make(chan struct{}), t: t}
	go c.readLoop(scanner)
	c.mustCall("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	return c
}

// TestProxy_initialize verifies that proxy mode responds to initialize and
// returns config and read in tools/list.
func TestProxy_initialize(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", filepath.Join(fixturesDir, "github"))
	raw := startProxyServer(t, cfg).mustCall("tools/list", nil)
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
	for _, want := range []string{"config", "read", "github__list_pull_requests"} {
		if !names[want] {
			t.Errorf("proxy tools/list missing %q, got: %v", want, names)
		}
	}
	for _, absent := range []string{"list", "call", "perm_call"} {
		if names[absent] {
			t.Errorf("proxy tools/list should not expose standard tool %q", absent)
		}
	}
}

// TestProxy_callUpstreamTool verifies that a proxy-mode tool call routes
// correctly to the upstream and returns a result.
func TestProxy_callUpstreamTool(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", filepath.Join(fixturesDir, "github"))
	client := startProxyServer(t, cfg)
	raw := client.mustCall("tools/call", map[string]any{
		"name":      "github__list_pull_requests",
		"arguments": map[string]any{},
	})
	text, isErr := parseToolCallResult(raw)
	if isErr {
		t.Fatalf("expected success, got error: %s", text)
	}
	if text == "" {
		t.Error("expected non-empty response from proxy tool call")
	}
}

// TestProxy_toolsListAnnotationsPassthrough verifies that annotations on an
// upstream tool are forwarded verbatim in proxy-mode tools/list.
func TestProxy_toolsListAnnotationsPassthrough(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{
		"do_thing":        `{"ok":true}`,
		"do_thing.schema": `{"annotations":{"readOnlyHint":true,"title":"Do Thing"}}`,
	})
	writeFakeServer(t, cfg, "svc", dir)

	raw := startProxyServer(t, cfg).mustCall("tools/list", nil)
	var result struct {
		Tools []struct {
			Name        string          `json:"name"`
			Annotations json.RawMessage `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	for _, tool := range result.Tools {
		if tool.Name != "svc__do_thing" {
			continue
		}
		var ann struct {
			ReadOnlyHint bool   `json:"readOnlyHint"`
			Title        string `json:"title"`
		}
		if err := json.Unmarshal(tool.Annotations, &ann); err != nil {
			t.Fatalf("unmarshal annotations %s: %v", tool.Annotations, err)
		}
		if !ann.ReadOnlyHint || ann.Title != "Do Thing" {
			t.Errorf("unexpected annotations: %s", tool.Annotations)
		}
		return
	}
	t.Error("svc__do_thing not found in proxy tools/list")
}

// TestServe_unreachableUpstreamDoesNotExit verifies that mini serve continues
// running when an upstream fails to connect at startup. Previously os.Exit(1)
// was called, which prevented startup when any server was unavailable.
func TestServe_unreachableUpstreamDoesNotExit(t *testing.T) {
	cfg := t.TempDir()
	// Valid upstream (will connect) + unreachable HTTP upstream
	writeFakeServer(t, cfg, "github", filepath.Join(fixturesDir, "github"))
	writeServerConfig(t, cfg, "dead",
		"name: dead\ntransport: http\nurl: http://127.0.0.1:19998\n") // nothing listening
	client := startServer(t, cfg)
	// Should still serve list/call for the working upstream
	text := client.listTools("github")
	if !strings.Contains(text, "list_pull_requests") {
		t.Errorf("expected github tools after partial startup failure, got: %q", text)
	}
}

// TestProxy_unreachableUpstreamDoesNotExit is the proxy-mode equivalent.
func TestProxy_unreachableUpstreamDoesNotExit(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "github", filepath.Join(fixturesDir, "github"))
	writeServerConfig(t, cfg, "dead",
		"name: dead\ntransport: http\nurl: http://127.0.0.1:19998\n")
	client := startProxyServer(t, cfg)
	raw := client.mustCall("tools/list", nil)
	var result struct {
		Tools []struct{ Name string `json:"name"` } `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range result.Tools {
		if tool.Name == "github__list_pull_requests" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected github tools in proxy mode after partial startup failure")
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

