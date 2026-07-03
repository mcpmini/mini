//go:build integration

package integration_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const canonicalSumCode = `async (input) => ({ total: input.values.reduce((a, b) => a + b, 0) })`

func requireDeno(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}
}

func codeModeConfig(t *testing.T, configDir string) {
	t.Helper()
	writeConfig(t, configDir, "experimental_code_mode: true\n")
}

func execCode(t *testing.T, c *mcpClient, code string, input any) (string, bool) {
	t.Helper()
	args := map[string]any{"code": code}
	if input != nil {
		args["input"] = input
	}
	raw := c.mustCall("tools/call", map[string]any{
		"name":      "execute_code",
		"arguments": args,
	})
	return parseToolCallResult(raw)
}

func toolListNames(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("parse tools/list: %v", err)
	}
	names := make([]string, len(result.Tools))
	for i, tool := range result.Tools {
		names[i] = tool.Name
	}
	return names
}

func hasTool(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func assertCanonicalSum(t *testing.T, c *mcpClient) {
	t.Helper()
	text, isErr := execCode(t, c, canonicalSumCode, map[string]any{"values": []any{1, 2, 3}})
	if isErr {
		t.Fatalf("expected success, got error: %s", text)
	}
	if text != `{"total":6}` {
		t.Errorf(`expected {"total":6}, got %s`, text)
	}
}

func TestExecuteCode_DisabledByDefault(t *testing.T) {
	cfg := t.TempDir()
	client := startServer(t, cfg)
	names := toolListNames(t, client.mustCall("tools/list", nil))
	if hasTool(names, "execute_code") {
		t.Errorf("expected execute_code absent by default, got: %v", names)
	}
}

// Unit tests construct config.Config directly, so only this test covers YAML
// loading of experimental_code_mode through the real binary.
func TestExecuteCode_EnabledViaConfig(t *testing.T) {
	requireDeno(t)
	cfg := t.TempDir()
	codeModeConfig(t, cfg)
	client := startServer(t, cfg)

	t.Run("tools/list contains execute_code", func(t *testing.T) {
		names := toolListNames(t, client.mustCall("tools/list", nil))
		if !hasTool(names, "execute_code") {
			t.Errorf("expected execute_code in tools/list, got: %v", names)
		}
	})

	t.Run("canonical call sums values", func(t *testing.T) {
		assertCanonicalSum(t, client)
	})
}

func TestExecuteCode_DaemonProxyPath(t *testing.T) {
	requireDeno(t)
	cfg := shortConfigDir(t)
	codeModeConfig(t, cfg)
	startDaemon(t, cfg)
	client := connectCompact(t, cfg)

	names := toolListNames(t, client.mustCall("tools/list", nil))
	if !hasTool(names, "execute_code") {
		t.Fatalf("expected execute_code in daemon tools/list, got: %v", names)
	}
	assertCanonicalSum(t, client)
}

func TestExecuteCode_SandboxDenialOverWire(t *testing.T) {
	requireDeno(t)
	cfg := t.TempDir()
	codeModeConfig(t, cfg)
	client := startServer(t, cfg)

	text, isErr := execCode(t, client, `async () => Deno.readTextFile("/etc/hosts")`, nil)
	if !isErr {
		t.Fatalf("expected isError=true for sandboxed file read, got success: %s", text)
	}
	if !strings.Contains(text, "NotCapable") {
		t.Errorf("expected error text to mention NotCapable, got: %s", text)
	}
}

func TestExecuteCode_CancellationOverWire(t *testing.T) {
	requireDeno(t)
	cfg := t.TempDir()
	codeModeConfig(t, cfg)
	client := startServer(t, cfg)

	id, resultCh := client.callAsync("tools/call", map[string]any{
		"name":      "execute_code",
		"arguments": map[string]any{"code": `async () => { while (true) {} }`},
	})
	client.notify("notifications/cancelled", map[string]any{"requestId": id})

	assertCancelledPromptly(t, resultCh)
}

func assertCancelledPromptly(t *testing.T, resultCh <-chan *mcpResult) {
	t.Helper()
	select {
	case r := <-resultCh:
		assertCancelledResult(t, r)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cancelled response")
	}
}

func assertCancelledResult(t *testing.T, r *mcpResult) {
	t.Helper()
	if r.err != nil {
		t.Fatalf("unexpected protocol error: %v", r.err)
	}
	text, isErr := parseToolCallResult(r.raw)
	if !isErr {
		t.Fatalf("expected isError=true after cancellation, got: %s", text)
	}
	if !strings.Contains(strings.ToLower(text), "cancel") {
		t.Errorf("expected error text to mention cancellation, got: %s", text)
	}
}
