//go:build integration

package integration_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/transport/transporttest"
)

func TestIntegrationDaemonProxyRelaysToolsListChangedAfterUpstreamMutation(t *testing.T) {
	t.Parallel()
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

func TestIntegrationDaemonProxyTagsToolsListChangedForModernListen(t *testing.T) {
	t.Parallel()
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

	stdin, scanner := startProxyCmd(t, cfg)
	lines := scanProxyLines(scanner)
	listen := transport.ListenParams{Notifications: transport.ListenNotifications{ToolsListChanged: true}}
	writeProxyLine(t, stdin, transporttest.ModernRequest("L", transport.MethodSubscriptionsListen, listen))
	waitForProxyLine(t, lines, transport.NotificationSubscriptionsAcknowledged)
	writeProxyLine(t, stdin, transporttest.ModernRequest(2, "tools/list", nil))
	waitForProxyLine(t, lines, `"svc__get_item"`)

	putFakeTool(t, fakeToolParams{
		ControlAddr: waitForControlFile(t, controlFile),
		Name:        "dynamic_tool",
		Content:     `{"id":2,"name":"after"}`,
	})
	changed := waitForProxyLine(t, lines, transport.NotificationToolsChanged)
	var notification transport.ToolsListChangedNotification
	if err := json.Unmarshal([]byte(changed), &notification); err != nil ||
		string(notification.Params.Meta.SubscriptionID) != `"L"` {
		t.Fatalf("tools/list_changed for a modern listen was not tagged: %s", changed)
	}
}

func scanProxyLines(scanner *bufio.Scanner) <-chan string {
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	return lines
}

func writeProxyLine(t *testing.T, stdin io.Writer, line []byte) {
	t.Helper()
	if _, err := stdin.Write(line); err != nil {
		t.Fatalf("write to proxy: %v", err)
	}
}

func waitForProxyLine(t *testing.T, lines <-chan string, want string) string {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("proxy output closed before %q", want)
			}
			if strings.Contains(line, want) {
				return line
			}
		case <-deadline:
			t.Fatalf("no proxy output containing %q", want)
		}
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
	writeFakeServer(t, p.ConfigDir, fakeServerParams{
		ServerName: p.ServerName,
		Fixtures:   p.Fixtures,
		ExtraArgs:  []string{"--control-file", p.ControlFile},
	})
}

func waitForControlFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path) //fileiolint:allow poll for the subprocess control address
		if err == nil &&
			strings.TrimSpace(string(data)) != "" {
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

func TestIntegrationToolListTTLRefreshesWithoutNotification(t *testing.T) {
	for _, mode := range []string{"standalone", "daemon"} {
		t.Run(mode, func(t *testing.T) {
			cfg := shortConfigDir(t)
			bin, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			configtest.WriteServer(t, cfg, config.ServerConfig{
				Name:    "svc",
				Command: bin,
				Args: []string{
					"-test.run=^TestIntegrationModernStdioHelper$",
					"--",
					"modern-stdio-helper",
					"ttl-only",
				},
			})
			client := startModernClientWithoutInitialize(t, cfg, mode)
			// The helper's list expires at once and it never sends list_changed, so only honoring the
			// TTL shows the agent the helper's second listing.
			got := client.callModern(t, "tools/list", nil)
			if !strings.Contains(string(got), `"name":"svc__new"`) ||
				strings.Contains(string(got), `"name":"svc__old"`) {
				t.Fatalf("tool list after expiry %s", got)
			}
		})
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
