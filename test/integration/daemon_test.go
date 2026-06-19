//go:build integration

package integration_test

import (
	"bufio"
	"encoding/json"
	"fmt"
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
)

func waitForDaemon(t *testing.T, portFile string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(portFile); err == nil {
			port, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			if port != 0 {
				resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
				if err == nil && resp.StatusCode == http.StatusOK {
					return port
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("daemon did not start within 5s")
	return 0
}

func startDaemon(t *testing.T, configDir string) int {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", configDir, "daemon", "--port", "0")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
		os.Remove(filepath.Join(configDir, "daemon.port"))
	})
	return waitForDaemon(t, filepath.Join(configDir, "daemon.port"))
}

func startProxyCmd(t *testing.T, configDir string) (io.WriteCloser, *bufio.Scanner) {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", configDir, "connect", "--log-level", "error")
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
	return stdin, scanner
}

func connectProxy(t *testing.T, configDir string) *mcpClient {
	t.Helper()
	stdin, scanner := startProxyCmd(t, configDir)
	c := &mcpClient{stdin: stdin, done: make(chan struct{}), t: t}
	go c.readLoop(scanner)
	c.mustCall("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	return c
}

func startCompactCmd(t *testing.T, configDir string) (io.WriteCloser, *bufio.Scanner) {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", configDir, "connect", "--tool-mode", "compact", "--log-level", "error")
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
	return stdin, scanner
}

func connectCompact(t *testing.T, configDir string) *mcpClient {
	t.Helper()
	stdin, scanner := startCompactCmd(t, configDir)
	c := &mcpClient{stdin: stdin, done: make(chan struct{}), t: t}
	go c.readLoop(scanner)
	c.mustCall("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	return c
}

// startKillableDaemon starts a daemon reading its port from config (daemon_port: 0 →
// OS-assigned) so a proxy respawn — which also reads config — behaves identically.
func startKillableDaemon(t *testing.T, configDir string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", configDir, "daemon")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
		os.Remove(filepath.Join(configDir, "daemon.port"))
	})
	waitForDaemon(t, filepath.Join(configDir, "daemon.port"))
	return cmd
}

func killDaemon(t *testing.T, cmd *exec.Cmd, portFile string) {
	t.Helper()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill daemon: %v", err)
	}
	cmd.Wait() //nolint:errcheck
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://127.0.0.1:" + portFromFile(t, portFile) + "/healthz")
		if err != nil {
			return
		}
		resp.Body.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("daemon still healthy after kill")
}

// killDaemonOnPort kills by PID, for a daemon the proxy respawned where the test has
// no *exec.Cmd to call killDaemon on.
func killDaemonOnPort(port string) {
	if port == "0" || port == "" {
		return
	}
	out, err := exec.Command("lsof", "-ti", "tcp:"+port).Output()
	if err != nil {
		return
	}
	for _, pidStr := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(pidStr); err == nil {
			syscall.Kill(pid, syscall.SIGKILL) //nolint:errcheck
		}
	}
}

func portFromFile(t *testing.T, portFile string) string {
	t.Helper()
	data, err := os.ReadFile(portFile)
	if err != nil {
		return "0"
	}
	return strings.TrimSpace(string(data))
}

func TestDaemon_recoversAfterDaemonKilledMidSession(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"name":"test"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\ndaemon_port: 0\n")

	cmd := startKillableDaemon(t, cfg)
	// Single proxy → single respawn → daemon.port holds the one respawned daemon's port.
	t.Cleanup(func() { killDaemonOnPort(portFromFile(t, filepath.Join(cfg, "daemon.port"))) })
	tokenBefore := readDaemonToken(t, cfg)
	client := connectCompact(t, cfg)
	if e := client.execEnvelope("svc", "get_item", nil); e.Error != "" {
		t.Fatalf("pre-kill call failed: %+v", e)
	}

	killDaemon(t, cmd, filepath.Join(cfg, "daemon.port"))

	e := client.execEnvelope("svc", "get_item", nil)
	if e.Error != "" {
		t.Fatalf("post-kill call did not recover: %+v", e)
	}
	if got := readDaemonToken(t, cfg); got != tokenBefore {
		t.Errorf("daemon token rotated across respawn: before=%q after=%q", tokenBefore, got)
	}
}

// freeTCPPort asks the OS for an unused loopback port, then releases it. There is a brief
// race between Close() and the daemon binding the port, but in an isolated test environment
// another process claiming the port in that window is rare enough to be tolerated.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() //nolint:errcheck
	return port
}

// startFixedPortDaemon starts a killable daemon bound to a fixed daemon_port so that a proxy
// respawn rebinds the same port — making real port-contention (the thundering herd) observable.
func startFixedPortDaemon(t *testing.T, configDir string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", configDir, "daemon")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	})
	waitForDaemon(t, filepath.Join(configDir, "daemon.port"))
	return cmd
}

func sigkillDaemon(t *testing.T, cmd *exec.Cmd, portFile string) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL daemon: %v", err)
	}
	cmd.Wait() //nolint:errcheck
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://127.0.0.1:" + portFromFile(t, portFile) + "/healthz")
		if err != nil {
			return
		}
		resp.Body.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("daemon still healthy after SIGKILL")
}

// TestDaemon_recoversAfterSIGKILL kills the daemon with SIGKILL so its deferred port-file
// removal never runs and daemon.port is left STALE. The proxy's next call must still recover
// (proving RunningPort's /healthz probe rejects the stale file rather than trusting it) and
// reuse the persisted token.
func TestDaemon_recoversAfterSIGKILL(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"name":"test"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	port := freeTCPPort(t)
	writeConfig(t, cfg, fmt.Sprintf("inline_threshold: 50000\ndaemon_port: %d\n", port))

	cmd := startFixedPortDaemon(t, cfg)
	portFile := filepath.Join(cfg, "daemon.port")
	t.Cleanup(func() { killDaemonListeners(port) })
	tokenBefore := readDaemonToken(t, cfg)
	client := connectCompact(t, cfg)
	if e := client.execEnvelope("svc", "get_item", nil); e.Error != "" {
		t.Fatalf("pre-kill call failed: %+v", e)
	}

	sigkillDaemon(t, cmd, portFile)
	if _, err := os.Stat(portFile); err != nil {
		t.Fatalf("expected stale port file to remain after SIGKILL, got: %v", err)
	}

	e := client.execEnvelope("svc", "get_item", nil)
	if e.Error != "" {
		t.Fatalf("post-SIGKILL call did not recover despite stale port file: %+v", e)
	}
	if got := readDaemonToken(t, cfg); got != tokenBefore {
		t.Errorf("daemon token rotated across SIGKILL respawn: before=%q after=%q", tokenBefore, got)
	}
}

// TestDaemon_manyClientsRecover is the scale test. N proxies share one fixed-port daemon;
// after it is SIGKILL'd, every proxy's next call must recover, the token must be reused, and
// exactly ONE daemon ends up bound to the port. This proves end-to-end single-winner recovery
// at scale. It does NOT isolate the flock spawn lock: the OS socket bind alone leaves one
// listener (losers fail EADDRINUSE and exit), so the test passes with or without the lock. The
// lock only collapses wasted spawn attempts, which the OS bind makes invisible here.
func TestDaemon_manyClientsRecover(t *testing.T) {
	const n = 20
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"name":"test"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	port := freeTCPPort(t)
	writeConfig(t, cfg, fmt.Sprintf("inline_threshold: 50000\ndaemon_port: %d\n", port))

	cmd := startFixedPortDaemon(t, cfg)
	portFile := filepath.Join(cfg, "daemon.port")
	t.Cleanup(func() { killDaemonListeners(port) })
	tokenBefore := readDaemonToken(t, cfg)

	clients := make([]*mcpClient, n)
	for i := range clients {
		clients[i] = connectCompact(t, cfg)
		if e := clients[i].execEnvelope("svc", "get_item", nil); e.Error != "" {
			t.Fatalf("client %d pre-kill call failed: %+v", i, e)
		}
	}

	sigkillDaemon(t, cmd, portFile)
	recoverAllClients(t, clients)

	if got := readDaemonToken(t, cfg); got != tokenBefore {
		t.Errorf("daemon token rotated across respawn: before=%q after=%q", tokenBefore, got)
	}
	if got := daemonListenerCount(t, port); got != 1 {
		t.Errorf("expected exactly one daemon listening on port %d after recovery, got %d", port, got)
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

func listenerPIDs(port int) []int {
	out, err := exec.Command("lsof", "-ti", "tcp:"+strconv.Itoa(port), "-sTCP:LISTEN").Output()
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var pids []int
	for _, s := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(s); err == nil && pid != self {
			pids = append(pids, pid)
		}
	}
	return pids
}

// daemonListenerCount returns how many distinct processes hold a *listening* socket on port.
// More than one means a respawn herd leaked extra daemons that lost the port bind. It is
// scoped to LISTEN sockets so the test's own healthz client connections are never counted.
func daemonListenerCount(t *testing.T, port int) int {
	t.Helper()
	return len(listenerPIDs(port))
}

// killDaemonListeners reaps daemons a proxy respawned (and that the test owns no cmd handle
// for). It targets only LISTEN sockets — never the test process's own client connections to
// the port — so it cannot SIGKILL the test binary itself.
func killDaemonListeners(port int) {
	for _, pid := range listenerPIDs(port) {
		syscall.Kill(pid, syscall.SIGKILL) //nolint:errcheck
	}
}

func TestDaemon_basicToolCall(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"name":"test"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")

	startDaemon(t, cfg)
	client := connectCompact(t, cfg)
	e := client.execEnvelope("svc", "get_item", nil)
	if e.Error != "" {
		t.Errorf("expected ok=true, got: %+v", e)
	}
}

func TestDaemon_sessionIsolation(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"secret":"x","name":"test"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")

	startDaemon(t, cfg)

	c1 := connectCompact(t, cfg)
	c2 := connectCompact(t, cfg)

	c1.setProjection("svc", "get_item", map[string]any{"exclude_always": []string{"secret"}}, true)

	b1, _ := json.Marshal(c1.execEnvelope("svc", "get_item", nil).Data)
	if strings.Contains(string(b1), "secret") {
		t.Errorf("c1: session projection should exclude secret, got: %s", b1)
	}

	b2, _ := json.Marshal(c2.execEnvelope("svc", "get_item", nil).Data)
	if !strings.Contains(string(b2), "secret") {
		t.Errorf("c2: different session should still have secret, got: %s", b2)
	}
}

func TestDaemon_standaloneFlag(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"ping": `{"ok":true}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")

	// No daemon running — --standalone should work without trying to start one
	client := startServer(t, cfg)
	e := client.execEnvelope("svc", "ping", nil)
	if e.Error != "" {
		t.Errorf("standalone mode: expected ok=true, got: %+v", e)
	}
}

func TestDaemon_healthzEndpoint(t *testing.T) {
	cfg := t.TempDir()
	writeConfig(t, cfg, "inline_threshold: 50000\n")

	port := startDaemon(t, cfg)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
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

func readDaemonToken(t *testing.T, configDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(configDir, "daemon.token"))
	if err != nil {
		t.Fatalf("read daemon token: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func initHTTPSession(t *testing.T, baseURL, token string) string {
	t.Helper()
	resp := daemonPost(t, baseURL, daemonPostOpts{Token: token, ToolMode: "compact"})
	sessionID := resp.Header.Get("Mcp-Session-Id")
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	if sessionID == "" {
		t.Fatal("expected Mcp-Session-Id from daemon")
	}
	return sessionID
}

func TestDaemon_HTTPClientDirect(t *testing.T) {
	baseURL, configDir := daemonBaseURL(t)
	token := readDaemonToken(t, configDir)
	sessionID := initHTTPSession(t, baseURL, token)
	resp := postHTTPToolCall(t, httpToolCall{baseURL: baseURL, sessionID: sessionID, token: token, server: "svc", tool: "get_item"})
	env := decodeDaemonEnvelope(t, resp)
	assertInlineGetItem(t, env)
}

func TestDaemon_HTTPRejectsMissingToken(t *testing.T) {
	baseURL, _ := daemonBaseURL(t)
	resp := daemonPost(t, baseURL, daemonPostOpts{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}
}

func TestDaemon_HTTPRejectsWrongToken(t *testing.T) {
	baseURL, _ := daemonBaseURL(t)
	resp := daemonPost(t, baseURL, daemonPostOpts{Token: "wrong-token-value"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", resp.StatusCode)
	}
}

func TestDaemon_HostHeaderRejection(t *testing.T) {
	baseURL, configDir := daemonBaseURL(t)
	token := readDaemonToken(t, configDir)
	resp := daemonPost(t, baseURL, daemonPostOpts{Token: token, Host: "evil.com"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-loopback Host, got %d", resp.StatusCode)
	}
}

func TestDaemon_CrossOriginRejection(t *testing.T) {
	baseURL, configDir := daemonBaseURL(t)
	token := readDaemonToken(t, configDir)
	resp := daemonPost(t, baseURL, daemonPostOpts{Token: token, Origin: "http://evil.com"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin request, got %d", resp.StatusCode)
	}
}

func TestDaemon_TokenFilePermissions(t *testing.T) {
	_, configDir := daemonBaseURL(t)
	fi, err := os.Stat(filepath.Join(configDir, "daemon.token"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected token file mode 0600, got %04o", perm)
	}
}

type daemonPostOpts struct {
	Token    string
	Host     string
	Origin   string
	ToolMode string
}

func daemonPost(t *testing.T, baseURL string, opts daemonPostOpts) *http.Response {
	t.Helper()
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	}
	if opts.ToolMode != "" {
		params["_mini_tool_mode"] = opts.ToolMode
	}
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": params,
	})
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	if opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}
	if opts.Host != "" {
		req.Host = opts.Host
	}
	if opts.Origin != "" {
		req.Header.Set("Origin", opts.Origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func daemonBaseURL(t *testing.T) (string, string) {
	t.Helper()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"name":"test"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")
	return fmt.Sprintf("http://127.0.0.1:%d", startDaemon(t, cfg)), cfg
}

type httpToolCall struct {
	baseURL   string
	sessionID string
	token     string
	server    string
	tool      string
}

func postHTTPToolCall(t *testing.T, c httpToolCall) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"server": c.server, "tool": c.tool}},
	})
	req, _ := http.NewRequest(http.MethodPost, c.baseURL+"/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", c.sessionID)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeDaemonEnvelope(t *testing.T, resp *http.Response) envelope {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	text := decodeRPCToolText(t, resp.Body)
	var env envelope
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("decode call envelope: %v\ntext: %s", err, text)
	}
	return env
}

func decodeRPCToolText(t *testing.T, body io.Reader) string {
	t.Helper()
	var rpc struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool                                  `json:"isError"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&rpc); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("unexpected rpc error: %#v", rpc.Error)
	}
	if rpc.Result.IsError || len(rpc.Result.Content) == 0 {
		t.Fatal("expected successful tools/call result")
	}
	return rpc.Result.Content[0].Text
}

func assertInlineGetItem(t *testing.T, env envelope) {
	t.Helper()
	if env.Error != "" {
		t.Fatalf("expected ok=true envelope, got %+v", env)
	}
	if env.File != nil {
		t.Fatalf("expected inline response, got file %q", *env.File)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected envelope data object, got %#v", env.Data)
	}
	if data["id"] != float64(1) {
		t.Fatalf("expected data.id=1, got %#v", data["id"])
	}
	if data["name"] != "test" {
		t.Fatalf("expected data.name=test, got %#v", data["name"])
	}
}

func TestDaemon_proxyModeToolCall(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"name":"test"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")

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
