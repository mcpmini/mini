//go:build integration

package integration_test

import (
	"context"
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

func requireCached(t *testing.T, specifiers ...string) {
	t.Helper()
	requireDeno(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append([]string{"cache"}, specifiers...)
	if out, err := exec.CommandContext(ctx, "deno", args...).CombinedOutput(); err != nil {
		t.Skipf("could not resolve %v (likely offline): %v\n%s", specifiers, err, out)
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

func TestExecuteCode_ImportsCachedPackage(t *testing.T) {
	requireCached(t, "jsr:@std/csv@1")
	cfg := t.TempDir()
	codeModeConfig(t, cfg)
	client := startServer(t, cfg)

	code := `async () => {
		const { parse } = await import("jsr:@std/csv@1");
		return parse("a,b\n1,2", { skipFirstRow: true });
	}`
	args := map[string]any{"code": code, "packages": []string{"jsr:@std/csv@1"}}
	raw := client.mustCall("tools/call", map[string]any{"name": "execute_code", "arguments": args})
	text, isErr := parseToolCallResult(raw)
	if isErr {
		t.Fatalf("expected success, got error: %s", text)
	}
	if text != `[{"a":"1","b":"2"}]` {
		t.Errorf(`expected [{"a":"1","b":"2"}], got %s`, text)
	}
}

func TestExecuteCode_PackagesThroughDaemonProxy(t *testing.T) {
	requireCached(t, "jsr:@std/csv@1")
	cfg := shortConfigDir(t)
	codeModeConfig(t, cfg)
	startDaemon(t, cfg)
	client := connectCompact(t, cfg)

	code := `async () => {
		const { parse } = await import("jsr:@std/csv@1");
		return parse("a,b\n1,2", { skipFirstRow: true });
	}`
	args := map[string]any{"code": code, "packages": []string{"jsr:@std/csv@1"}}
	raw := client.mustCall("tools/call", map[string]any{"name": "execute_code", "arguments": args})
	text, isErr := parseToolCallResult(raw)
	if isErr {
		t.Fatalf("expected success through daemon, got error: %s", text)
	}
	if text != `[{"a":"1","b":"2"}]` {
		t.Errorf(`expected [{"a":"1","b":"2"}], got %s`, text)
	}
}

func TestExecuteCode_UnresolvablePackageFailsOverWire(t *testing.T) {
	requireDeno(t)
	cfg := t.TempDir()
	codeModeConfig(t, cfg)
	client := startServer(t, cfg)

	args := map[string]any{"code": "async () => 1", "packages": []string{"npm:@mini-forge-test/nope-xyz"}}
	raw := client.mustCall("tools/call", map[string]any{"name": "execute_code", "arguments": args})
	text, isErr := parseToolCallResult(raw)
	if !isErr {
		t.Fatalf("expected isError=true for unresolvable package, got success: %s", text)
	}
	if !strings.Contains(text, "dependency") {
		t.Errorf("expected error text to mention dependency, got: %s", text)
	}
}

func TestExecuteCode_WireSchemaDeclaresInputTypeUnionAndPackages(t *testing.T) {
	cfg := t.TempDir()
	codeModeConfig(t, cfg)
	client := startServer(t, cfg)

	input, packages := executeCodeWireSchema(t, client.mustCall("tools/list", nil))
	if types, _ := input["type"].([]any); len(types) == 0 {
		t.Errorf("input.type must be an explicit type union — untyped properties get string-encoded by some MCP clients — got: %v", input["type"])
	}
	if packages == nil {
		t.Error("expected packages property in execute_code wire schema")
	}
}

func executeCodeWireSchema(t *testing.T, raw json.RawMessage) (input, packages map[string]any) {
	t.Helper()
	var result struct {
		Tools []struct {
			Name        string         `json:"name"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("parse tools/list: %v", err)
	}
	for _, tool := range result.Tools {
		if tool.Name != "execute_code" {
			continue
		}
		props, _ := tool.InputSchema["properties"].(map[string]any)
		input, _ = props["input"].(map[string]any)
		packages, _ = props["packages"].(map[string]any)
		if input == nil {
			t.Fatal("execute_code wire schema has no input property")
		}
		return input, packages
	}
	t.Fatal("execute_code not found in tools/list")
	return nil, nil
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
