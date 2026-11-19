//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestIntegrationLifecycle_addServerAtRuntime(t *testing.T) {
	t.Parallel()
	f := newFakeHTTPMCP(t, nil)
	cfg := t.TempDir()
	fixtureConfig := config.DefaultConfig()
	fixtureConfig.DangerousAllowPrivateURLs = true
	configtest.WriteConfig(t, cfg, fixtureConfig)

	client := startServer(t, cfg)

	raw := client.mustCall("tools/call", map[string]any{
		"name": "config",
		"arguments": map[string]any{
			"action": "add_server",
			"config": map[string]any{"name": "svc", "transport": "sse", "url": f.srv.URL},
		},
	})
	var r struct {
		IsError bool `json:"isError"`
	}
	json.Unmarshal(raw, &r) //nolint:errcheck
	if r.IsError {
		t.Fatalf("add_server failed: %s", raw)
	}

	if !strings.Contains(client.listTools(), "get_item") {
		t.Error("expected get_item in list after add_server")
	}
}

func TestIntegrationLifecycle_removeServerAtRuntime(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	control := filepath.Join(cfg, "svc-control")
	writeFakeServerWithControlFile(t, fakeServerControlParams{
		ConfigDir: cfg, ServerName: "svc", Fixtures: mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`}),
		ControlFile: control,
	})
	client := startServer(t, cfg)

	if !strings.Contains(client.listTools(), "get_item") {
		t.Fatal("setup: expected get_item in list before remove")
	}

	client.mustCall("tools/call", map[string]any{
		"name":      "config",
		"arguments": map[string]any{"action": "remove_server", "server": "svc"},
	})

	_, isErr := client.nativeCallError("svc", "get_item")
	if !isErr {
		t.Error("expected error after server removed")
	}
	waitUntilFakeExited(t, waitForControlFile(t, control))
}

func TestIntegrationLifecycle_anUpstreamThatDiesMidSession_isReconnected(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	control := filepath.Join(cfg, "svc-control")
	writeFakeServerWithControlFile(t, fakeServerControlParams{
		ConfigDir: cfg, ServerName: "svc", Fixtures: mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`}),
		ControlFile: control,
	})
	client := startServer(t, cfg)
	upstream := &FakeMCPControl{addr: waitForControlFile(t, control), t: t}

	upstream.SetFault(map[string]any{"method": "tools/call", "type": "connection_drop"})

	if client.callSucceeds("svc", "get_item") {
		t.Fatal("a call to an upstream that exits mid-call succeeded")
	}
	client.waitUntilCallSucceeds("svc", "get_item")
}

func TestIntegrationDaemon_anEditWhileTheFirstConnectIsStuck_isServedAndOutlivesTheStuckConnect(t *testing.T) {
	t.Parallel()
	cfg := shortConfigDir(t)
	stuckControl := filepath.Join(cfg, "stuck-control")
	writeFaultServer(t, faultServerParams{
		ConfigDir: cfg, ServerName: "svc", Fixtures: mockFixtureDir(t, map[string]string{"get_old": `{}`}),
		FaultJSON: initializeStuckPastOneReloadTick, HandshakeTimeout: "60s", ControlFile: stuckControl,
	})
	startHealthyDaemon(t, cfg)
	stuck := waitForControlFile(t, stuckControl)

	writeFakeServer(
		t,
		cfg,
		fakeServerParams{ServerName: "svc", Fixtures: mockFixtureDir(t, map[string]string{"get_new": `{}`})},
	)
	client := connectProxy(t, cfg)
	client.sendNotification("notifications/initialized", nil)
	waitUntilListed(t, client, listing{tool: "svc__get_new", listed: true})

	waitUntilFakeExited(t, stuck)
	if tools := proxyToolNames(
		t,
		client,
	); containsString(tools, "svc__get_old") ||
		!containsString(tools, "svc__get_new") {
		t.Errorf("tools after the stuck connect ended = %v, want svc__get_new and not svc__get_old", tools)
	}
}

func TestIntegrationDaemonSIGTERM_withAStuckConnectAndAReconnectedUpstream_exitsAndLeavesNoUpstream(t *testing.T) {
	t.Parallel()
	cfg := shortConfigDir(t)
	stuckControl, droppedControl := filepath.Join(cfg, "stuck-control"), filepath.Join(cfg, "dropped-control")
	writeFaultServer(t, faultServerParams{
		ConfigDir: cfg, ServerName: "stuck", Fixtures: mockFixtureDir(t, map[string]string{"never": `{}`}),
		FaultJSON: stuckInitialize, HandshakeTimeout: "60s", ControlFile: stuckControl,
	})
	writeFakeServerWithControlFile(t, fakeServerControlParams{
		ConfigDir: cfg, ServerName: "dropped", Fixtures: mockFixtureDir(t, map[string]string{"get_item": `{}`}),
		ControlFile: droppedControl,
	})
	daemon := startHealthyDaemon(t, cfg)
	client := connectProxy(t, cfg)
	client.sendNotification("notifications/initialized", nil)
	waitUntilListed(t, client, listing{tool: "dropped__get_item", listed: true})
	first := waitForControlFile(t, droppedControl)
	(&FakeMCPControl{addr: first, t: t}).SetFault(map[string]any{"method": "tools/call", "type": "connection_drop"})
	if client.callSucceeds("dropped", "get_item") {
		t.Fatal("setup: a call to an upstream that exits mid-call succeeded")
	}
	reconnected := waitForNewControlAddr(t, droppedControl, first)

	if err := daemon.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	waitForExit(t, daemon, daemonDrainBound)
	waitUntilFakeExited(t, waitForControlFile(t, stuckControl))
	waitUntilFakeExited(t, reconnected)
}

// waitForNewControlAddr returns the address a respawned fakemcp writes over the old one.
func waitForNewControlAddr(t *testing.T, path, old string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if addr := waitForControlFile(t, path); addr != old {
			return addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("no new fakemcp process wrote %s", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestIntegrationLifecycle_addServerBadURL(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	fixtureConfig := config.DefaultConfig()
	fixtureConfig.DangerousAllowPrivateURLs = true
	configtest.WriteConfig(t, cfg, fixtureConfig)

	client := startServer(t, cfg)

	raw := client.mustCall("tools/call", map[string]any{
		"name": "config",
		"arguments": map[string]any{
			"action": "add_server",
			"config": map[string]any{"name": "bad", "transport": "sse", "url": "not-a-url"},
		},
	})
	var r struct {
		IsError bool `json:"isError"`
	}
	json.Unmarshal(raw, &r) //nolint:errcheck
	if !r.IsError {
		t.Error("expected error for bad URL")
	}
}

func TestIntegrationLifecycle_disabledServerNotLoaded(t *testing.T) {
	t.Parallel()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	cfg := t.TempDir()
	disabled := false
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "disabled", Fixtures: dir, Enabled: &disabled})

	client := startServer(t, cfg)
	_, isErr := client.nativeCallError("disabled", "get_item")
	if !isErr {
		t.Error("call to disabled server should return error")
	}
	if strings.Contains(client.listTools(), "disabled") {
		t.Error("disabled server tools should not appear in list")
	}
}

func TestIntegrationLifecycle_tenServersSimultaneously(t *testing.T) {
	t.Parallel()
	cfg := t.TempDir()
	for i := range 10 {
		dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
		name := fmt.Sprintf("svc%d", i)
		writeFakeServer(t, cfg, fakeServerParams{ServerName: name, Fixtures: dir})
	}

	client := startServer(t, cfg)
	listing := client.listTools()
	if !strings.Contains(listing, "svc0") || !strings.Contains(listing, "svc9") {
		t.Errorf("expected svc0..svc9 in listing, got: %s", listing[:min(200, len(listing))])
	}
}

const stuckInitialize = `{"method":"initialize","type":"slow_initialize","delay_ms":30000}`

const daemonDrainBound = 10 * time.Second

// Outlasts one 5s config poll, yet ends inside waitUntilFakeExited's 10s.
const initializeStuckPastOneReloadTick = `{"method":"initialize","type":"slow_initialize","delay_ms":9000}`

func fakeIsRunning(controlAddr string) bool {
	resp, err := http.Get("http://" + controlAddr + "/faults")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func waitUntilFakeExited(t *testing.T, controlAddr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for fakeIsRunning(controlAddr) {
		if time.Now().After(deadline) {
			t.Fatalf("the fakemcp process with control address %s is still running", controlAddr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (c *mcpClient) callSucceeds(server, tool string) bool {
	raw, err := c.call("tools/call", toolCallRaw(toolCallParams{Server: server, Tool: tool, Args: map[string]any{}}))
	if err != nil {
		return false
	}
	_, isErr := parseToolCallResult(raw)
	return !isErr
}

func (c *mcpClient) waitUntilCallSucceeds(server, tool string) {
	c.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !c.callSucceeds(server, tool) {
		if time.Now().After(deadline) {
			c.t.Fatalf("%s__%s still fails; mini did not reconnect %s", server, tool, server)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForExit(t *testing.T, cmd *exec.Cmd, bound time.Duration) {
	t.Helper()
	exited := testutil.RunInBackground(cmd.Wait)
	select {
	case <-exited:
	case <-time.After(bound):
		t.Fatalf("the daemon did not exit within %s of SIGTERM", bound)
	}
}
