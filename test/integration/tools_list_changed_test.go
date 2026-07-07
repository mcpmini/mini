//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDaemonProxyRelaysToolsListChangedAfterUpstreamMutation(t *testing.T) {
	cfg := shortConfigDir(t)
	fixtures := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"name":"before"}`})
	controlFile := filepath.Join(cfg, "actual-upstream-control")
	writeFakeServerWithControlFile(t, fakeServerControlParams{
		ConfigDir:   cfg,
		ServerName:  "svc",
		Fixtures:    fixtures,
		ControlFile: controlFile,
	})
	startDaemon(t, cfg)

	client := connectProxy(t, cfg)
	client.sendNotification("notifications/initialized", nil)
	initial := proxyToolNames(t, client)
	if !containsString(initial, "svc__get_item") {
		t.Fatalf("initial tools/list missing svc__get_item: %v", initial)
	}

	controlAddr := waitForControlFile(t, controlFile)
	putFakeTool(t, fakeToolParams{
		ControlAddr: controlAddr,
		Name:        "dynamic_tool",
		Content:     `{"id":2,"name":"after"}`,
	})

	client.waitForNotification("notifications/tools/list_changed", 5*time.Second)
	refreshed := proxyToolNames(t, client)
	if !containsString(refreshed, "svc__dynamic_tool") {
		t.Fatalf("refreshed tools/list missing svc__dynamic_tool: %v", refreshed)
	}
}

type fakeServerControlParams struct {
	ConfigDir   string
	ServerName  string
	Fixtures    string
	ControlFile string
}

func writeFakeServerWithControlFile(t *testing.T, p fakeServerControlParams) {
	t.Helper()
	dir := filepath.Join(p.ConfigDir, "servers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	yaml := fmt.Sprintf("name: %s\ncommand: %s\nargs:\n  - --fixtures\n  - %s\n  - --control-file\n  - %s\n", p.ServerName, fakemcpBin, p.Fixtures, p.ControlFile)
	writeStringFile(t, filepath.Join(dir, p.ServerName+".yaml"), yaml)
}

func waitForControlFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) != "" {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("control file %s was not written", path)
	return ""
}

type fakeToolParams struct {
	ControlAddr string
	Name        string
	Content     string
}

func putFakeTool(t *testing.T, p fakeToolParams) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"ToolDefinition": map[string]any{
			"name":        p.Name,
			"description": p.Name,
			"inputSchema": map[string]any{"type": "object"},
		},
		"Content": p.Content,
	})
	req, _ := http.NewRequest(http.MethodPut, "http://"+p.ControlAddr+"/tools", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put fake tool: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put fake tool status = %d", resp.StatusCode)
	}
}

func proxyToolNames(t *testing.T, client *mcpClient) []string {
	t.Helper()
	raw := client.mustCall("tools/list", nil)
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("parse tools/list: %v\nraw: %s", err, raw)
	}
	names := make([]string, len(result.Tools))
	for i, tool := range result.Tools {
		names[i] = tool.Name
	}
	return names
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
