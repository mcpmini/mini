//go:build integration

package integration_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/daemon"
	"github.com/mcpmini/mini/internal/testutil"
)

func shortConfigDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mini")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) }) //nolint:errcheck
	return dir
}

func socketPath(cfg string) string { return daemon.SocketPath(cfg) }

func daemonHTTPClient(cfg string) *http.Client {
	sock := socketPath(cfg)
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
}

func daemonHealthy(cfg string) bool {
	resp, err := daemonHTTPClient(cfg).Get("http://localhost/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func waitForDaemon(t *testing.T, cfg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if daemonHealthy(cfg) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("daemon did not become healthy within 5s")
}

func startDaemon(t *testing.T, cfg string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", cfg, "daemon")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
		reapDaemons(cfg)
	})
	waitForDaemon(t, cfg)
	return cmd
}

func killDaemonProc(t *testing.T, cmd *exec.Cmd, cfg string, sig syscall.Signal) {
	t.Helper()
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatalf("signal daemon: %v", err)
	}
	cmd.Wait() //nolint:errcheck
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !daemonHealthy(cfg) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon still healthy after %v", sig)
}

func daemonPIDs(t *testing.T, cfg string) []int {
	t.Helper()
	// Proxy processes carry "<cfg> connect", so matching "<cfg> daemon" counts only daemons.
	out, err := exec.Command("pgrep", "-f", cfg+" daemon").Output()
	if err != nil {
		return nil // pgrep exits 1 when nothing matches
	}
	var pids []int
	for _, s := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(s); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

func reapDaemons(cfg string) {
	out, err := exec.Command("pgrep", "-f", cfg+" daemon").Output()
	if err != nil {
		return
	}
	for _, s := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(s); err == nil {
			syscall.Kill(pid, syscall.SIGKILL) //nolint:errcheck
		}
	}
}

func readDaemonToken(t *testing.T, cfg string) string {
	t.Helper()
	data := testutil.ReadFile(t, filepath.Join(cfg, "internal", "daemon", "daemon.token"))
	return strings.TrimSpace(string(data))
}

func daemonForTest(t *testing.T) string {
	t.Helper()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"name":"test"}`})
	cfg := shortConfigDir(t)
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})
	return cfg
}

func startProxyCmd(t *testing.T, cfg, toolMode string) (io.WriteCloser, *bufio.Scanner) {
	t.Helper()
	args := []string{"--config", cfg, "connect", "--log-level", "error"}
	if toolMode != "" {
		args = append(args, "--tool-mode", toolMode)
	}
	cmd := exec.Command(miniBin, args...)
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
		stdin.Close()      //nolint:errcheck
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	})
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4<<20), 4<<20)
	return stdin, scanner
}

func connect(t *testing.T, cfg, toolMode string) *mcpClient {
	t.Helper()
	stdin, scanner := startProxyCmd(t, cfg, toolMode)
	c := newMCPClient(t, stdin, scanner)
	c.mustCall("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	return c
}

func connectProxy(t *testing.T, cfg string) *mcpClient   { return connect(t, cfg, "") }
func connectCompact(t *testing.T, cfg string) *mcpClient { return connect(t, cfg, "compact") }

func TestIntegrationDaemon_basicToolCall(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	client := connectCompact(t, cfg)
	if e := client.execEnvelope("svc", "get_item", nil); e.Error != "" {
		t.Errorf("expected ok=true, got: %+v", e)
	}
}

func TestIntegrationDaemon_spawnsOnDemandAndReuses(t *testing.T) {
	cfg := daemonForTest(t)
	t.Cleanup(func() { reapDaemons(cfg) })

	c1 := connectProxy(t, cfg)
	c1.mustCall("tools/list", map[string]any{})
	if !daemonHealthy(cfg) {
		t.Fatal("first connect did not spawn a daemon")
	}
	c2 := connectProxy(t, cfg)
	c2.mustCall("tools/list", map[string]any{})
	if got := len(daemonPIDs(t, cfg)); got != 1 {
		t.Errorf("expected exactly 1 daemon shared by two proxies, got %d", got)
	}
}

func TestIntegrationDaemon_sessionIsolation(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"secret":"x","name":"test"}`})
	cfg := shortConfigDir(t)
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})

	startDaemon(t, cfg)
	c1 := connectCompact(t, cfg)
	c2 := connectCompact(t, cfg)

	c1.setProjection("svc", "get_item", map[string]any{"exclude": []string{"secret"}}, true)

	b1, _ := json.Marshal(c1.execEnvelope("svc", "get_item", nil).Data)
	if strings.Contains(string(b1), "secret") {
		t.Errorf("c1: session projection should exclude secret, got: %s", b1)
	}
	b2, _ := json.Marshal(c2.execEnvelope("svc", "get_item", nil).Data)
	if !strings.Contains(string(b2), "secret") {
		t.Errorf("c2: different session should still have secret, got: %s", b2)
	}
}

func TestIntegrationDaemon_standaloneFlag(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"ping": `{"ok":true}`})
	cfg := shortConfigDir(t)
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})

	// No daemon running — --standalone should work without trying to start one.
	client := startServer(t, cfg)
	if e := client.execEnvelope("svc", "ping", nil); e.Error != "" {
		t.Errorf("standalone mode: expected ok=true, got: %+v", e)
	}
}

// /healthz keys on HTTP server readiness, not upstream connectivity (#33).
func TestIntegrationDaemon_healthyBeforeSlowUpstreamConnects(t *testing.T) {
	cfg := shortConfigDir(t)
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	fault := map[string]any{"method": "initialize", "type": "slow_initialize", "delay_ms": 5000}
	faultJSON, _ := json.Marshal(fault)
	writeFaultServer(t, faultServerParams{
		ConfigDir:        cfg,
		ServerName:       "slow",
		Fixtures:         dir,
		FaultJSON:        string(faultJSON),
		HandshakeTimeout: "1s",
	})

	start := time.Now()
	startDaemon(t, cfg)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("daemon took %v to become healthy; want well under the upstream's 5s slow_initialize delay", elapsed)
	}
}

// #322: Codex keeps the first tools/list it gets, so a cold start must answer it with a slow server's tools.
func TestIntegrationDaemon_coldStartFirstToolsListIncludesASlowServer(t *testing.T) {
	cfg := shortConfigDir(t)
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	fault := map[string]any{"method": "initialize", "type": "slow_initialize", "delay_ms": 1500}
	faultJSON, _ := json.Marshal(fault) //nolint:errcheck // a map of strings and ints always encodes
	writeFaultServer(
		t,
		faultServerParams{ConfigDir: cfg, ServerName: "slow", Fixtures: dir, FaultJSON: string(faultJSON)},
	)
	t.Cleanup(func() { reapDaemons(cfg) })

	raw := connectProxy(t, cfg).mustCall("tools/list", map[string]any{})

	if !strings.Contains(string(raw), "slow__get_item") {
		t.Errorf("first tools/list on a cold start = %s, want the slow server's tools", raw)
	}
}

func TestIntegrationDaemon_healthzEndpoint(t *testing.T) {
	cfg := shortConfigDir(t)
	startDaemon(t, cfg)

	resp, err := daemonHTTPClient(cfg).Get("http://localhost/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body) //nolint:errcheck
	if body["ok"] != true {
		t.Errorf("healthz should return ok=true, got: %v", body)
	}
}

func TestIntegrationDaemon_proxyModeToolCall(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	client := connectProxy(t, cfg)
	raw := client.mustCall("tools/call", map[string]any{
		"name":      "svc__get_item",
		"arguments": map[string]any{},
	})
	text, isErr := parseToolCallResult(raw)
	if isErr {
		t.Fatalf("proxy mode tool call returned error: %s", text)
	}
	if text == "" {
		t.Error("expected non-empty response from proxy mode tool call via daemon")
	}
}

func TestIntegrationDaemon_recoversAfterGracefulKill(t *testing.T) {
	cfg := daemonForTest(t)
	cmd := startDaemon(t, cfg)
	tokenBefore := readDaemonToken(t, cfg)
	client := connectCompact(t, cfg)
	if e := client.execEnvelope("svc", "get_item", nil); e.Error != "" {
		t.Fatalf("pre-kill call failed: %+v", e)
	}

	killDaemonProc(t, cmd, cfg, syscall.SIGTERM)

	if e := client.execEnvelope("svc", "get_item", nil); e.Error != "" {
		t.Fatalf("post-kill call did not recover: %+v", e)
	}
	if got := readDaemonToken(t, cfg); got != tokenBefore {
		t.Errorf("daemon token rotated across respawn: before=%q after=%q", tokenBefore, got)
	}
}

func TestIntegrationDaemon_recoversAfterSIGKILLWithStaleSocket(t *testing.T) {
	cfg := daemonForTest(t)
	cmd := startDaemon(t, cfg)
	tokenBefore := readDaemonToken(t, cfg)
	client := connectCompact(t, cfg)
	if e := client.execEnvelope("svc", "get_item", nil); e.Error != "" {
		t.Fatalf("pre-kill call failed: %+v", e)
	}

	killDaemonProc(t, cmd, cfg, syscall.SIGKILL)
	// SIGKILL has no clean Close to unlink the socket, so the file is left stale; recovery must clear it.
	if _, err := os.Stat(socketPath(cfg)); err != nil {
		t.Fatalf("expected stale socket file to remain after SIGKILL, got: %v", err)
	}

	if e := client.execEnvelope("svc", "get_item", nil); e.Error != "" {
		t.Fatalf("post-SIGKILL call did not recover despite stale socket: %+v", e)
	}
	if got := readDaemonToken(t, cfg); got != tokenBefore {
		t.Errorf("daemon token rotated across SIGKILL respawn: before=%q after=%q", tokenBefore, got)
	}
}

func TestIntegrationDaemon_manyClientsRecoverSingleWinner(t *testing.T) {
	const n = 20
	cfg := daemonForTest(t)
	cmd := startDaemon(t, cfg)
	t.Cleanup(func() { reapDaemons(cfg) })
	tokenBefore := readDaemonToken(t, cfg)

	clients := make([]*mcpClient, n)
	for i := range clients {
		clients[i] = connectCompact(t, cfg)
		if e := clients[i].execEnvelope("svc", "get_item", nil); e.Error != "" {
			t.Fatalf("client %d pre-kill call failed: %+v", i, e)
		}
	}

	killDaemonProc(t, cmd, cfg, syscall.SIGKILL)
	recoverAllClients(t, clients)

	if got := readDaemonToken(t, cfg); got != tokenBefore {
		t.Errorf("daemon token rotated across respawn: before=%q after=%q", tokenBefore, got)
	}
	// net.Listen on the socket guarantees one daemon survives the herd; the flock only collapses wasted spawns.
	if got := len(daemonPIDs(t, cfg)); got != 1 {
		t.Errorf("expected exactly one daemon after herd recovery, got %d", got)
	}
}

func recoverAllClients(t *testing.T, clients []*mcpClient) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make([]string, len(clients))
	for i, c := range clients {
		wg.Add(1)
		go func(i int, c *mcpClient) {
			defer wg.Done()
			if e := c.execEnvelope("svc", "get_item", nil); e.Error != "" {
				errs[i] = e.Error
			}
		}(i, c)
	}
	wg.Wait()
	for i, msg := range errs {
		if msg != "" {
			t.Errorf("client %d did not recover after kill: %s", i, msg)
		}
	}
}

func TestIntegrationDaemon_removedServerFile_dropsToolsWithoutRestart(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	client := connectProxy(t, cfg)
	client.sendNotification("notifications/initialized", nil)
	waitUntilListed(t, client, listing{tool: "svc__get_item", listed: true})

	if err := os.Remove(filepath.Join(cfg, "servers", "svc.yaml")); err != nil {
		t.Fatal(err)
	}

	waitUntilListed(t, client, listing{tool: "svc__get_item", listed: false})
}

type listing struct {
	tool   string
	listed bool
}

func waitUntilListed(t *testing.T, client *mcpClient, want listing) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for containsString(proxyToolNames(t, client), want.tool) != want.listed {
		client.waitForNotification("notifications/tools/list_changed", time.Until(deadline))
	}
}
