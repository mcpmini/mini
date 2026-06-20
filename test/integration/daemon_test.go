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

func socketPath(cfg string) string { return filepath.Join(cfg, "daemon.sock") }

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
	data, err := os.ReadFile(filepath.Join(cfg, "daemon.token"))
	if err != nil {
		t.Fatalf("read daemon token: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func daemonForTest(t *testing.T) string {
	t.Helper()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"name":"test"}`})
	cfg := shortConfigDir(t)
	writeFakeServer(t, cfg, "svc", dir)
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
	c := &mcpClient{stdin: stdin, done: make(chan struct{}), t: t}
	go c.readLoop(scanner)
	c.mustCall("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	return c
}

func connectProxy(t *testing.T, cfg string) *mcpClient   { return connect(t, cfg, "") }
func connectCompact(t *testing.T, cfg string) *mcpClient { return connect(t, cfg, "compact") }

func TestDaemon_basicToolCall(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	client := connectCompact(t, cfg)
	if e := client.execEnvelope("svc", "get_item", nil); e.Error != "" {
		t.Errorf("expected ok=true, got: %+v", e)
	}
}

func TestDaemon_spawnsOnDemandAndReuses(t *testing.T) {
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

func TestDaemon_sessionIsolation(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"secret":"x","name":"test"}`})
	cfg := shortConfigDir(t)
	writeFakeServer(t, cfg, "svc", dir)

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
	cfg := shortConfigDir(t)
	writeFakeServer(t, cfg, "svc", dir)

	// No daemon running — --standalone should work without trying to start one.
	client := startServer(t, cfg)
	if e := client.execEnvelope("svc", "ping", nil); e.Error != "" {
		t.Errorf("standalone mode: expected ok=true, got: %+v", e)
	}
}

func TestDaemon_healthzEndpoint(t *testing.T) {
	cfg := t.TempDir()
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

func TestDaemon_proxyModeToolCall(t *testing.T) {
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

func TestDaemon_recoversAfterGracefulKill(t *testing.T) {
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

func TestDaemon_recoversAfterSIGKILLWithStaleSocket(t *testing.T) {
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

func TestDaemon_manyClientsRecoverSingleWinner(t *testing.T) {
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

func initHTTPSession(t *testing.T, cfg, token string) string {
	t.Helper()
	resp := daemonPost(t, cfg, daemonPostOpts{Token: token, ToolMode: "compact"})
	sessionID := resp.Header.Get("Mcp-Session-Id")
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	if sessionID == "" {
		t.Fatal("expected Mcp-Session-Id from daemon")
	}
	return sessionID
}

func TestDaemon_HTTPClientDirect(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	token := readDaemonToken(t, cfg)
	sessionID := initHTTPSession(t, cfg, token)
	resp := postHTTPToolCall(t, cfg, httpToolCall{sessionID: sessionID, token: token, server: "svc", tool: "get_item"})
	assertInlineGetItem(t, decodeDaemonEnvelope(t, resp))
}

func TestDaemon_HTTPRejectsMissingToken(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	resp := daemonPost(t, cfg, daemonPostOpts{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}
}

func TestDaemon_HTTPRejectsWrongToken(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	resp := daemonPost(t, cfg, daemonPostOpts{Token: "wrong-token-value"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", resp.StatusCode)
	}
}

func TestDaemon_HostHeaderRejection(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	resp := daemonPost(t, cfg, daemonPostOpts{Token: readDaemonToken(t, cfg), Host: "evil.com"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-loopback Host, got %d", resp.StatusCode)
	}
}

func TestDaemon_CrossOriginRejection(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	resp := daemonPost(t, cfg, daemonPostOpts{Token: readDaemonToken(t, cfg), Origin: "http://evil.com"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin request, got %d", resp.StatusCode)
	}
}

func TestDaemon_socketAndDirArePrivate(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)

	si, err := os.Stat(socketPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if si.Mode()&os.ModeSocket == 0 {
		t.Fatalf("expected a Unix socket, got mode %v", si.Mode())
	}
	// Linux honors the socket file's own mode on connect.
	if perm := si.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("socket is group/other-accessible: %04o", perm)
	}
	di, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// macOS ignores the socket file's mode on connect, so the directory's mode is the boundary there.
	if perm := di.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("socket dir is group/other-accessible: %04o", perm)
	}
}

func TestDaemon_TokenFilePermissions(t *testing.T) {
	cfg := daemonForTest(t)
	startDaemon(t, cfg)
	fi, err := os.Stat(filepath.Join(cfg, "daemon.token"))
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

func daemonPost(t *testing.T, cfg string, opts daemonPostOpts) *http.Response {
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
	req, _ := http.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader(string(body)))
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
	resp, err := daemonHTTPClient(cfg).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}



type httpToolCall struct {
	sessionID string
	token     string
	server    string
	tool      string
}

func postHTTPToolCall(t *testing.T, cfg string, c httpToolCall) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"server": c.server, "tool": c.tool}},
	})
	req, _ := http.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", c.sessionID)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := daemonHTTPClient(cfg).Do(req)
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

