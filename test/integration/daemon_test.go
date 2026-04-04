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
	"strconv"
	"strings"
	"testing"
	"time"
)

// startDaemon launches mini daemon in the background and waits for it to be ready.
// Returns the port number. Registers t.Cleanup to kill the process.
// startDaemon launches a daemon with --port 0 so the OS assigns a free port.
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

// connectProxy starts a mini serve instance that connects to the daemon at port.
func startProxyCmd(t *testing.T, configDir string) (io.WriteCloser, *bufio.Scanner) {
	t.Helper()
	cmd := exec.Command(miniBin, "--config", configDir, "serve", "--log-level", "error")
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

func TestDaemon_basicToolCall(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"name":"test"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")

	startDaemon(t, cfg)
	client := connectProxy(t, cfg)
	e := client.execEnvelope("svc", "get_item", nil)
	if !e.OK {
		t.Errorf("expected ok=true, got: %+v", e)
	}
}

func TestDaemon_sessionIsolation(t *testing.T) {
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"secret":"x","name":"test"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")

	startDaemon(t, cfg)

	c1 := connectProxy(t, cfg)
	c2 := connectProxy(t, cfg)

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
	if !e.OK {
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

func initHTTPSession(t *testing.T, baseURL string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "http-test", "version": "0"},
		},
	})
	resp, err := http.Post(baseURL+"/mcp", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := resp.Header.Get("Mcp-Session-Id")
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	if sessionID == "" {
		t.Fatal("expected Mcp-Session-Id from daemon")
	}
	return sessionID
}

func TestDaemon_HTTPClientDirect(t *testing.T) {
	baseURL := daemonBaseURL(t)
	sessionID := initHTTPSession(t, baseURL)
	resp := postHTTPToolCall(t, baseURL, sessionID, "svc", "get_item")
	env := decodeDaemonEnvelope(t, resp)
	assertInlineGetItem(t, env)
}

func daemonBaseURL(t *testing.T) string {
	t.Helper()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1,"name":"test"}`})
	cfg := t.TempDir()
	writeFakeServer(t, cfg, "svc", dir)
	writeConfig(t, cfg, "inline_threshold: 50000\n")
	return fmt.Sprintf("http://127.0.0.1:%d", startDaemon(t, cfg))
}

func postHTTPToolCall(t *testing.T, baseURL, sessionID, server, tool string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{"server": server, "tool": tool}},
	})
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", sessionID)
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
	if !env.OK {
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
