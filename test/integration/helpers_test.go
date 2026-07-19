//go:build integration

package integration_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	miniBin         string
	fakemcpBin      string
	fixturesDir     string
	expectedVersion string
)

func TestMain(m *testing.M) {
	root := moduleRoot()
	fixturesDir = filepath.Join(root, "benchmarks", "fixtures")

	var err error
	expectedVersion, err = gitVersion(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "git version: %v\n", err)
		os.Exit(1)
	}
	miniBin, err = buildBin(buildBinParams{
		root: root, name: "mini", pkg: "./cmd/mini",
		ldflags: "-X github.com/mcpmini/mini/internal/version.buildRevision=" + expectedVersion,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "build mini: %v\n", err)
		os.Exit(1)
	}
	fakemcpBin, err = buildBin(buildBinParams{root: root, name: "fakemcp", pkg: "./test/fakemcp"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "build fakemcp: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// gitVersion computes the revision string injected into the mini binary via
// -ldflags, so TestCLI_version can assert on it exactly.
func gitVersion(root string) (string, error) {
	rev, err := gitOutput(root, "rev-parse", "--short=7", "HEAD")
	if err != nil {
		return "", err
	}
	status, err := gitOutput(root, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", err
	}
	if status != "" {
		rev += "+dirty"
	}
	return rev, nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func moduleRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

type buildBinParams struct {
	root    string
	name    string
	pkg     string
	ldflags string
}

func buildBin(p buildBinParams) (string, error) {
	tmp, err := os.MkdirTemp("", "mini-inttest-*")
	if err != nil {
		return "", err
	}
	out := filepath.Join(tmp, p.name)
	args := []string{"build", "-tags", "integration", "-o", out}
	if p.ldflags != "" {
		args = append(args, "-ldflags", p.ldflags)
	}
	args = append(args, p.pkg)
	cmd := exec.Command("go", args...)
	cmd.Dir = p.root
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v\n%s", err, b)
	}
	return out, nil
}

func cliArgs(configDir string, args []string) []string {
	return append([]string{"--config", configDir}, args...)
}

func writeStringFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func fakeServerYAML(serverName, fixtures string) string {
	return fmt.Sprintf("name: %s\ncommand: %s\nargs:\n  - --fixtures\n  - %s\n",
		serverName, fakemcpBin, fixtures)
}

func toolCallRaw(serverTool string, server, tool string, args map[string]any) map[string]any {
	return map[string]any{
		"name": serverTool,
		"arguments": map[string]any{
			"server": server,
			"tool":   tool,
			"args":   args,
		},
	}
}

func parseToolCallResult(raw json.RawMessage) (string, bool) {
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	json.Unmarshal(raw, &result) //nolint:errcheck
	if len(result.Content) == 0 {
		return "", result.IsError
	}
	return result.Content[0].Text, result.IsError
}

func runCLI(t *testing.T, configDir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(miniBin, cliArgs(configDir, args)...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			exitCode = exit.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func runCLIWithStdin(t *testing.T, stdin string, configDir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(miniBin, cliArgs(configDir, args)...)
	cmd.Stdin = strings.NewReader(stdin)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			exitCode = exit.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func writeFakeServer(t *testing.T, configDir, serverName, fixtures string) {
	t.Helper()
	dir := filepath.Join(configDir, "servers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	writeStringFile(t, filepath.Join(dir, serverName+".yaml"), fakeServerYAML(serverName, fixtures))
}

type FakeMCPControl struct {
	addr string
	t    *testing.T
}

func startFakeMCPProcess(t *testing.T, fixtures string) io.Reader {
	t.Helper()
	cmd := exec.Command(fakemcpBin, "--fixtures", fixtures)
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin, err = os.Open(os.DevNull) // fakemcp uses stdin for MCP protocol traffic
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() }) //nolint:errcheck
	return stderrPipe
}

func startFakeMCP(t *testing.T, configDir, serverName, fixtures string) *FakeMCPControl {
	t.Helper()
	dir := filepath.Join(configDir, "servers")
	os.MkdirAll(dir, 0700) //nolint:errcheck
	addr := readControlAddr(t, startFakeMCPProcess(t, fixtures))
	os.WriteFile(filepath.Join(dir, serverName+".yaml"), []byte(fakeServerYAML(serverName, fixtures)), 0600) //nolint:errcheck
	return &FakeMCPControl{addr: addr, t: t}
}

func scanForControlAddr(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if after, ok := strings.CutPrefix(scanner.Text(), "fakemcp control="); ok {
			return after, nil
		}
	}
	return "", fmt.Errorf("fakemcp did not print control address")
}

func readControlAddr(t *testing.T, r io.Reader) string {
	t.Helper()
	type result struct {
		addr string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		addr, err := scanForControlAddr(r)
		ch <- result{addr, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatal(res.err)
		}
		return res.addr
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for fakemcp control address")
		return ""
	}
}

func (c *FakeMCPControl) SetFault(f map[string]any) {
	c.t.Helper()
	body, _ := json.Marshal(f)
	resp, err := http.Post("http://"+c.addr+"/fault", "application/json", strings.NewReader(string(body)))
	if err != nil {
		c.t.Fatalf("SetFault: %v", err)
	}
	resp.Body.Close()
}

func (c *FakeMCPControl) ClearFaults() {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, "http://"+c.addr+"/fault", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("ClearFaults: %v", err)
	}
	resp.Body.Close()
}

type mcpResult struct {
	raw json.RawMessage
	err error
}

type mcpMessage struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type mcpClient struct {
	stdin   io.WriteCloser
	pending sync.Map // int64 → chan *mcpResult
	nextID  atomic.Int64
	notify  chan string
	done    chan struct{}
	t       *testing.T
}

func startMiniCmd(t *testing.T, configDir string) (io.WriteCloser, *bufio.Scanner) {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", configDir, "connect", "--standalone", "--tool-mode", "compact", "--log-level", "error")
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

func startServer(t *testing.T, configDir string) *mcpClient {
	t.Helper()
	stdin, scanner := startMiniCmd(t, configDir)
	c := newMCPClient(t, stdin, scanner)
	c.mustCall("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
	waitForUpstreamsSettled(t, c)
	return c
}

// waitForUpstreamsSettled polls until the tool catalog reading is stable.
// Upstreams connect asynchronously (#33); call this before asserting tool presence
// to avoid racing the connect goroutines.
func waitForUpstreamsSettled(t *testing.T, c *mcpClient) {
	t.Helper()
	settleUntil(t, func() string { return c.listTools("") }, nil)
}

// waitForProxyUpstreamsSettled polls until at least one upstream tool (identified
// by the "__" naming convention) is stable in the proxy tools list.
func waitForProxyUpstreamsSettled(t *testing.T, c *mcpClient) {
	t.Helper()
	settleUntil(t, func() string { return string(c.mustCall("tools/list", nil)) },
		func(s string) bool { return strings.Contains(s, "__") })
}

// settleUntil polls snapshot() until ready(cur) is true for stableReadsRequired
// consecutive reads. When ready is nil any stable reading counts.
// Calls t.Fatalf on ceiling expiry only when ready is non-nil.
func settleUntil(t *testing.T, snapshot func() string, ready func(string) bool) {
	t.Helper()
	const stableReadsRequired = 3
	const pollInterval = 30 * time.Millisecond
	const ceiling = 3 * time.Second

	deadline := time.Now().Add(ceiling)
	last, stable := "", 0
	for time.Now().Before(deadline) {
		cur := snapshot()
		if ready == nil || ready(cur) {
			if cur == last {
				stable++
				if stable >= stableReadsRequired {
					return
				}
			} else {
				stable = 1
				last = cur
			}
		} else {
			last, stable = cur, 0
		}
		time.Sleep(pollInterval)
	}
	if ready != nil {
		t.Fatalf("catalog did not settle within %s; last snapshot: %s", ceiling, last)
	}
}

func newMCPClient(t *testing.T, stdin io.WriteCloser, scanner *bufio.Scanner) *mcpClient {
	t.Helper()
	c := &mcpClient{stdin: stdin, notify: make(chan string, 32), done: make(chan struct{}), t: t}
	go c.readLoop(scanner)
	return c
}

func (c *mcpClient) readLoop(scanner *bufio.Scanner) {
	defer close(c.done)
	for scanner.Scan() {
		var msg mcpMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if string(msg.ID) == "null" || len(msg.ID) == 0 {
			var notification struct {
				Method string `json:"method"`
			}
			if json.Unmarshal(scanner.Bytes(), &notification) == nil && notification.Method != "" {
				select {
				case c.notify <- notification.Method:
				default:
				}
			}
			continue
		}
		var id int64
		if err := json.Unmarshal(msg.ID, &id); err != nil {
			continue
		}
		r := &mcpResult{raw: msg.Result}
		if msg.Error != nil {
			r.err = fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message)
		}
		if ch, ok := c.pending.LoadAndDelete(id); ok {
			ch.(chan *mcpResult) <- r
		}
	}
}

func (c *mcpClient) sendNotification(method string, params any) {
	c.t.Helper()
	p, _ := json.Marshal(params)
	req := map[string]any{"jsonrpc": "2.0", "method": method, "params": json.RawMessage(p)}
	b, _ := json.Marshal(req)
	fmt.Fprintf(c.stdin, "%s\n", b)
}

func (c *mcpClient) waitForNotification(method string, timeout time.Duration) {
	c.t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case got := <-c.notify:
			if got == method {
				return
			}
		case <-c.done:
			c.t.Fatalf("connection closed waiting for notification %q", method)
		case <-deadline:
			c.t.Fatalf("timed out waiting for notification %q", method)
		}
	}
}

func (c *mcpClient) mustCall(method string, params any) json.RawMessage {
	c.t.Helper()
	result, err := c.call(method, params)
	if err != nil {
		c.t.Fatalf("call %s: %v", method, err)
	}
	return result
}

func (c *mcpClient) call(method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan *mcpResult, 1)
	c.pending.Store(id, ch)

	p, _ := json.Marshal(params)
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": json.RawMessage(p)}
	b, _ := json.Marshal(req)
	fmt.Fprintf(c.stdin, "%s\n", b)

	select {
	case r := <-ch:
		return r.raw, r.err
	case <-c.done:
		return nil, fmt.Errorf("connection closed waiting for response to %s", method)
	}
}

func (c *mcpClient) setProjection(server, tool string, proj map[string]any, sessionOnly bool) {
	c.t.Helper()
	c.mustCall("tools/call", map[string]any{
		"name": "config",
		"arguments": map[string]any{
			"action":       "set_projection",
			"server":       server,
			"tool":         tool,
			"session_only": sessionOnly,
			"projection":   proj,
		},
	})
}

func (c *mcpClient) execTool(server, tool string, args map[string]any) string {
	c.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	raw := c.mustCall("tools/call", map[string]any{
		"name": "call",
		"arguments": map[string]any{
			"server": server,
			"tool":   tool,
			"args":   args,
		},
	})
	return toolCallText(c.t, raw)
}

type proxyResult struct {
	Data any              `json:"data"`
	Mini *proxyResultMini `json:"__mini"`
}

type proxyResultMini struct {
	Msg         string           `json:"msg"`
	File        string           `json:"file"`
	Excluded    []string         `json:"excluded"`
	Truncated   []miniTruncation `json:"truncated"`
	Passthrough map[string]any   `json:"passthrough"`
}

func proxyToolArguments(args, controls map[string]any) map[string]any {
	arguments := map[string]any{"args": args}
	if controls != nil {
		arguments["__mini"] = controls
	}
	return arguments
}

func (c *mcpClient) execProxyTool(serverDotTool string, args, controls map[string]any) proxyResult {
	c.t.Helper()
	raw := c.mustCall("tools/call", map[string]any{
		"name":      serverDotTool,
		"arguments": proxyToolArguments(args, controls),
	})
	text := toolCallText(c.t, raw)
	var pr proxyResult
	if err := json.Unmarshal([]byte(text), &pr); err != nil {
		c.t.Fatalf("parse proxy result: %v\ntext: %s", err, text)
	}
	return pr
}

// execProxyToolRaw sends arguments verbatim, bypassing the {"args": ...} wrapper —
// for testing rejection of legacy/malformed argument shapes. A rejected legacy
// call surfaces as a JSON-RPC protocol error (invalid params), not a tool-level
// isError result, so this uses call (not mustCall) and reports either shape.
func (c *mcpClient) execProxyToolRaw(serverDotTool string, rawArguments map[string]any) (string, bool) {
	c.t.Helper()
	raw, err := c.call("tools/call", map[string]any{
		"name":      serverDotTool,
		"arguments": rawArguments,
	})
	if err != nil {
		return err.Error(), true
	}
	return parseToolCallResult(raw)
}

func (c *mcpClient) execProxyToolAllowError(serverDotTool string, args, controls map[string]any) (string, bool) {
	c.t.Helper()
	raw := c.mustCall("tools/call", map[string]any{
		"name":      serverDotTool,
		"arguments": proxyToolArguments(args, controls),
	})
	return parseToolCallResult(raw)
}

func (c *mcpClient) listTools(server string) string {
	c.t.Helper()
	raw := c.mustCall("tools/call", map[string]any{
		"name":      "list",
		"arguments": map[string]any{"server": server},
	})
	return toolCallText(c.t, raw)
}

func toolCallText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("parse tool result: %v\nraw: %s", err, raw)
	}
	if result.IsError && len(result.Content) > 0 {
		t.Fatalf("tool returned error: %s", result.Content[0].Text)
	}
	if len(result.Content) == 0 {
		return ""
	}
	return result.Content[0].Text
}

type envelope struct {
	Data        any            `json:"data"`
	Excluded    []string       `json:"excluded"`
	Truncated   []truncation   `json:"truncated"`
	File        *string        `json:"file"`
	Passthrough map[string]any `json:"passthrough"`
	Error       string         `json:"error"`
	Message     string         `json:"message"`
	Retryable   bool           `json:"retryable"`
}

type truncation struct {
	JQPath string `json:"path"`
	Chars  int    `json:"chars"`
	Items  int    `json:"items"`
}

func (c *mcpClient) execEnvelope(server, tool string, args map[string]any) envelope {
	c.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	raw := c.mustCall("tools/call", toolCallRaw("call", server, tool, args))
	// Use parseToolCallResult (not toolCallText) so we get the content even when
	// mini returns isError=true — error envelopes are still valid JSON in the content.
	text, _ := parseToolCallResult(raw)
	var e envelope
	if err := json.Unmarshal([]byte(text), &e); err != nil {
		c.t.Fatalf("parse envelope from call %s.%s: %v\ntext: %s", server, tool, err, text)
	}
	return e
}

type miniEnv struct {
	File      string           `json:"file"`
	Excluded  []string         `json:"excluded"`
	Truncated []miniTruncation `json:"truncated"`
}

type miniTruncation struct {
	Path  string `json:"path"`
	Chars int    `json:"chars"`
	Items int    `json:"items"`
}

func parseMiniEnv(t *testing.T, text string) miniEnv {
	t.Helper()
	var outer struct {
		Mini miniEnv `json:"__mini"`
	}
	if err := json.Unmarshal([]byte(text), &outer); err != nil {
		t.Fatalf("parse __mini response: %v\ntext: %s", err, text)
	}
	if outer.Mini.File == "" {
		t.Fatalf("expected __mini.file in response, got: %s", text)
	}
	return outer.Mini
}

func (c *mcpClient) callRead(file, filter string) string {
	c.t.Helper()
	args := map[string]any{"file": file}
	if filter != "" {
		args["filter"] = filter
	}
	raw := c.mustCall("tools/call", map[string]any{
		"name":      "read",
		"arguments": args,
	})
	return toolCallText(c.t, raw)
}

func (c *mcpClient) execProtectedTool(server, tool string, args map[string]any) string {
	c.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	raw := c.mustCall("tools/call", map[string]any{
		"name": "perm_call",
		"arguments": map[string]any{
			"server": server,
			"tool":   tool,
			"args":   args,
		},
	})
	return toolCallText(c.t, raw)
}

func (c *mcpClient) execProtectedAllowError(server, tool string, args map[string]any) (string, bool) {
	c.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	raw := c.mustCall("tools/call", toolCallRaw("perm_call", server, tool, args))
	return parseToolCallResult(raw)
}

func (c *mcpClient) execToolAllowError(server, tool string, args map[string]any) (string, bool) {
	c.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	raw := c.mustCall("tools/call", toolCallRaw("call", server, tool, args))
	return parseToolCallResult(raw)
}

// quickServerWith starts a server with fixture files, optional global config YAML, and optional projection YAML.
func quickServerWith(t *testing.T, fixtures map[string]string, cfgYAML, projYAML string) *mcpClient {
	t.Helper()
	dir := mockFixtureDir(t, fixtures)
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	if cfgYAML != "" {
		writeConfig(t, cfg, cfgYAML)
		return startQuickServer(t, cfg, projYAML)
	}
	return startQuickServer(t, cfg, projYAML)
}

// quickServer starts a server with fixture files and default config.
func quickServer(t *testing.T, fixtures map[string]string) *mcpClient {
	t.Helper()
	dir := mockFixtureDir(t, fixtures)
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	return startServer(t, cfg)
}

func startQuickServer(t *testing.T, cfg, projYAML string) *mcpClient {
	t.Helper()
	if projYAML != "" {
		writeProjection(t, cfg, "svc", projYAML)
	}
	return startServer(t, cfg)
}

func faultServer(t *testing.T, fixtures map[string]string, fault map[string]any, toolTimeout string) *mcpClient {
	t.Helper()
	dir := mockFixtureDir(t, fixtures)
	cfg := t.TempDir()
	faultJSON, _ := json.Marshal(fault)
	writeFaultServer(t, faultServerParams{ConfigDir: cfg, ServerName: "svc", Fixtures: dir, FaultJSON: string(faultJSON), ToolTimeout: toolTimeout})
	return startServer(t, cfg)
}

type faultServerParams struct {
	ConfigDir   string
	ServerName  string
	Fixtures    string
	FaultJSON   string
	ToolTimeout string
	Extra       string
}

func writeFaultServer(t *testing.T, p faultServerParams) {
	t.Helper()
	dir := filepath.Join(p.ConfigDir, "servers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	yaml := fmt.Sprintf("name: %s\ncommand: %s\nargs:\n  - --fixtures\n  - %s\n  - --initial-fault\n  - '%s'\n",
		p.ServerName, fakemcpBin, p.Fixtures, p.FaultJSON)
	if p.ToolTimeout != "" {
		yaml += "tool_timeout: " + p.ToolTimeout + "\n"
	}
	yaml += p.Extra
	if err := os.WriteFile(filepath.Join(dir, p.ServerName+".yaml"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
}

func mockFixtureDir(t *testing.T, fixtures map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range fixtures {
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func writeConfig(t *testing.T, configDir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeProjection(t *testing.T, configDir, serverName, content string) {
	t.Helper()
	dir := filepath.Join(configDir, "servers")
	os.MkdirAll(dir, 0700)
	if err := os.WriteFile(filepath.Join(dir, serverName+".proj.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeAction(t *testing.T, configDir, content string, name string) {
	t.Helper()
	dir := filepath.Join(configDir, "internal", "actions")
	os.MkdirAll(dir, 0700)
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeServerConfig(t *testing.T, configDir, name, yaml string) {
	t.Helper()
	dir := filepath.Join(configDir, "servers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
}

func backdateFile(t *testing.T, path string, age time.Duration) {
	t.Helper()
	ts := time.Now().Add(-age)
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatalf("backdate %s: %v", path, err)
	}
}

func writeOversizedFile(t *testing.T, limitBytes int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oversized.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte(`{"mcpServers": {}}`)) //nolint:errcheck
	f.Write(make([]byte, limitBytes+1))   //nolint:errcheck
	f.Close()
	return path
}

func mustUnmarshal(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func addServerViaRPC(t *testing.T, client *mcpClient, name, url string) (isErr bool, text string) {
	t.Helper()
	raw := client.mustCall("tools/call", map[string]any{
		"name": "config",
		"arguments": map[string]any{
			"action": "add_server",
			"config": map[string]any{"name": name, "transport": "sse", "url": url},
		},
	})
	txt, isErr := parseToolCallResult(raw)
	return isErr, txt
}

func writeServerYAML(t *testing.T, configDir, serverName, fixtures, extra string) {
	t.Helper()
	dir := filepath.Join(configDir, "servers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	yaml := "name: " + serverName + "\ncommand: " + fakemcpBin +
		"\nargs:\n  - --fixtures\n  - " + fixtures + "\n" + extra
	if err := os.WriteFile(filepath.Join(dir, serverName+".yaml"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
}
