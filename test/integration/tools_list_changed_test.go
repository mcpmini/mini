//go:build integration

package integration_test

import (
	"encoding/json"
	"testing"
	"time"
)

func TestToolsListChanged_DaemonProxyRelaysStdioUpstreamUpdates(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	cfg := shortConfigDir(t)
	control := startFakeMCP(t, cfg, "svc", dir)
	c1 := connectProxy(t, cfg)
	c2 := connectProxy(t, cfg)

	assertHasTool(t, proxyToolNames(t, c1), "svc__get_item")
	assertMissingTool(t, proxyToolNames(t, c1), "svc__added")

	control.AddTool("added", `{"ok":true}`)
	c1.waitForNotification("notifications/tools/list_changed", 5*time.Second)
	assertHasTool(t, proxyToolNames(t, c1), "svc__added")
	c2.waitForNotification("notifications/tools/list_changed", 5*time.Second)
	assertHasTool(t, proxyToolNames(t, c2), "svc__added")

	control.RemoveTool("get_item")
	c1.waitForNotification("notifications/tools/list_changed", 5*time.Second)
	assertMissingTool(t, proxyToolNames(t, c1), "svc__get_item")
	c2.waitForNotification("notifications/tools/list_changed", 5*time.Second)
	assertMissingTool(t, proxyToolNames(t, c2), "svc__get_item")
}

func TestToolsListChanged_DaemonProxyRelaysHTTPUpstreamUpdates(t *testing.T) {
	initial := []map[string]any{toolSchema("get_item")}
	upstream := newFakeHTTPNotifierMCP(t, initial)
	cfg := shortConfigDir(t)
	writeHTTPServerYAML(t, cfg, "svc", upstream.srv.URL)
	client := connectProxy(t, cfg)

	assertHasTool(t, proxyToolNames(t, client), "svc__get_item")
	assertMissingTool(t, proxyToolNames(t, client), "svc__added")

	upstream.setTools([]map[string]any{toolSchema("added")})
	client.waitForNotification("notifications/tools/list_changed", 5*time.Second)
	names := proxyToolNames(t, client)
	assertHasTool(t, names, "svc__added")
	assertMissingTool(t, names, "svc__get_item")
}

func TestToolsListChanged_DaemonProxyRefreshesMultipageCatalog(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"first": `{"id":1}`, "second": `{"id":2}`})
	cfg := shortConfigDir(t)
	control := startFakeMCPWithPageSize(t, cfg, "svc", dir, 1)
	client := connectProxy(t, cfg)

	assertHasTool(t, proxyToolNames(t, client), "svc__first")
	control.AddTool("third", `{"id":3}`)
	client.waitForNotification("notifications/tools/list_changed", 5*time.Second)

	names := proxyToolNames(t, client)
	assertHasTool(t, names, "svc__first")
	assertHasTool(t, names, "svc__second")
	assertHasTool(t, names, "svc__third")
}

func proxyToolNames(t *testing.T, client *mcpClient) []string {
	t.Helper()
	raw := client.mustCall("tools/list", map[string]any{})
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("parse tools/list: %v\nraw: %s", err, raw)
	}
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func toolSchema(name string) map[string]any {
	return map[string]any{
		"name":        name,
		"description": name,
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func assertHasTool(t *testing.T, names []string, want string) {
	t.Helper()
	for _, name := range names {
		if name == want {
			return
		}
	}
	t.Fatalf("missing tool %q in %v", want, names)
}

func assertMissingTool(t *testing.T, names []string, want string) {
	t.Helper()
	for _, name := range names {
		if name == want {
			t.Fatalf("unexpected tool %q in %v", want, names)
		}
	}
}
