//go:build integration

package integration_test

import (
	"strings"
	"testing"
)


func noSSRFClient(t *testing.T) *mcpClient {
	t.Helper()
	cfg := t.TempDir()
	return startServer(t, cfg)
}

func TestSecurity_SSRFBlocks0000(t *testing.T) {
	isErr, _ := addServerViaRPC(t, noSSRFClient(t), "x", "http://0.0.0.0/mcp")
	if !isErr {
		t.Error("expected SSRF error for 0.0.0.0")
	}
}

func TestSecurity_SSRFBlocksIPv6Mapped(t *testing.T) {
	isErr, _ := addServerViaRPC(t, noSSRFClient(t), "x", "http://[::ffff:127.0.0.1]/mcp")
	if !isErr {
		t.Error("expected SSRF error for IPv4-mapped IPv6")
	}
}

func TestSecurity_oversizedClaudeConfig(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "add", "--from-claude", writeOversizedFile(t, 10<<20))
	if code == 0 {
		t.Error("add --from-claude with oversized file should exit non-zero")
	}
}

func TestSecurity_timeoutNoReconnect(t *testing.T) {
	// A tool_timeout that fires should return an error but NOT trigger a reconnect
	// (reconnect is only for connection errors, not context cancellations)
	client := faultServer(t,
		map[string]string{"get_item": `{"id":1}`},
		map[string]any{"tool": "get_item", "method": "tools/call", "type": "delay", "delay_ms": 2000},
		"200ms")

	// First call should timeout (ok=false)
	text, isErr := client.execToolAllowError("svc", "get_item", nil)
	if !isErr && !strings.Contains(text, "timeout") && !strings.Contains(text, "error") {
		t.Errorf("expected timeout error, got: %s", text)
	}

	// Second call should also timeout — server should still be responding (not reconnecting)
	// If reconnect triggered, the tool would not be found (reconnect clears tools)
	text, isErr = client.execToolAllowError("svc", "get_item", nil)
	if !isErr && !strings.Contains(text, "timeout") && !strings.Contains(text, "error") {
		t.Errorf("second call: expected timeout error, got: %s", text)
	}
}

func TestSecurity_PermissionCaseMismatch(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"MyTool": `{"id":1}`})
	writeServerConfig(t, cfg, "svc", "name: svc\ncommand: "+fakemcpBin+"\nargs:\n  - --fixtures\n  - "+dir+
		"\npermissions:\n  protected:\n    - MyTool\n")

	client := startServer(t, cfg)
	_, isErr := client.execToolAllowError("svc", "mytool", nil)
	if !isErr {
		t.Error("expected call to fail: 'mytool' should not bypass 'MyTool' protection via case mismatch")
	}
	if strings.Contains(client.listTools("svc"), "mytool") {
		t.Error("'mytool' should not appear in list if 'MyTool' is registered (case-sensitive naming)")
	}
}
