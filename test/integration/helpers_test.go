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
	miniBin     string
	fakemcpBin  string
	fixturesDir string
)

func TestMain(m *testing.M) {
	root := moduleRoot()
	fixturesDir = filepath.Join(root, "benchmarks", "fixtures")

	var err error
	miniBin, err = buildBin(root, "mini", "./cmd/mini")
	if err != nil {
		fmt.Fprintf(os.Stderr, "build mini: %v\n", err)
		os.Exit(1)
	}
	fakemcpBin, err = buildBin(root, "fakemcp", "./test/fakemcp")
	if err != nil {
		fmt.Fprintf(os.Stderr, "build fakemcp: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func moduleRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func buildBin(root, name, pkg string) (string, error) {
	tmp, err := os.MkdirTemp("", "mini-inttest-*")
	if err != nil {
		return "", err
	}
	out := filepath.Join(tmp, name)
	cmd := exec.Command("go", "build", "-tags", "integration", "-o", out, pkg)
	cmd.Dir = root
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
	done    chan struct{}
	t       *testing.T
}

func startMiniCmd(t *testing.T, configDir string) (io.WriteCloser, *bufio.Scanner) {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", configDir, "serve", "--standalone", "--log-level", "error")
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
	c := &mcpClient{stdin: stdin, done: make(chan struct{}), t: t}
	go c.readLoop(scanner)
	c.mustCall("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "0"},
	})
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

type omission struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

type envelope struct {
	Data        any            `json:"data"`
	Elided      []string       `json:"elided"`
	Omitted     []omission     `json:"omitted"`
	Hint        string         `json:"hint"`
	File        *string        `json:"file"`
	Passthrough map[string]any `json:"passthrough"`
	Error       string         `json:"error"`
	Message     string         `json:"message"`
	Retryable   bool           `json:"retryable"`
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
	writeConfig(t, cfg, "inline_threshold: 50000\n")
	return startQuickServer(t, cfg, projYAML)
}

// quickServer starts a server with fixture files and default config.
func quickServer(t *testing.T, fixtures map[string]string) *mcpClient {
	t.Helper()
	dir := mockFixtureDir(t, fixtures)
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")
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
	writeConfig(t, cfg, "inline_threshold: 50000\n")
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
	dir := filepath.Join(configDir, "projections")
	os.MkdirAll(dir, 0700)
	if err := os.WriteFile(filepath.Join(dir, serverName+".yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeAction(t *testing.T, configDir, content string, name string) {
	t.Helper()
	dir := filepath.Join(configDir, "actions")
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
