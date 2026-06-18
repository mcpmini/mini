//go:build integration

package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLI_version(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		stdout, _, code := runCLI(t, t.TempDir(), args...)
		if code != 0 {
			t.Errorf("%v exited %d", args, code)
		}
		if v := strings.TrimSpace(stdout); v != expectedVersion {
			t.Errorf("%v output %q, want %q", args, v, expectedVersion)
		}
	}
}

func TestCLI_ls_empty(t *testing.T) {
	stdout, _, code := runCLI(t, t.TempDir(), "ls")
	if code != 0 {
		t.Errorf("ls with empty config should exit 0, got %d", code)
	}
	if !strings.Contains(stdout, "no servers") {
		t.Errorf("expected 'no servers' in output, got: %q", stdout)
	}
}

func TestCLI_add_then_ls(t *testing.T) {
	cfg := t.TempDir()
	runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp")
	stdout, _, code := runCLI(t, cfg, "ls")
	if code != 0 || !strings.Contains(stdout, "myserver") {
		t.Errorf("ls after add: code=%d, output=%q", code, stdout)
	}
}

func TestCLI_add_url_createsFile(t *testing.T) {
	cfg := t.TempDir()
	_, _, code := runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp")
	if code != 0 {
		t.Fatalf("add should exit 0, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(cfg, "servers", "myserver.yaml")); err != nil {
		t.Errorf("expected servers/myserver.yaml to exist: %v", err)
	}
}

func TestCLI_add_command_createsFile(t *testing.T) {
	cfg := t.TempDir()
	_, _, code := runCLI(t, cfg, "add", "myserver", "npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp")
	if code != 0 {
		t.Fatalf("add with command should exit 0, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(cfg, "servers", "myserver.yaml")); err != nil {
		t.Errorf("expected servers/myserver.yaml to exist: %v", err)
	}
}

func TestCLI_rm(t *testing.T) {
	cfg := t.TempDir()
	runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp")
	_, _, code := runCLI(t, cfg, "rm", "myserver")
	if code != 0 {
		t.Errorf("rm should exit 0, got %d", code)
	}
	stdout, _, _ := runCLI(t, cfg, "ls")
	if strings.Contains(stdout, "myserver") {
		t.Errorf("myserver should not appear in ls after rm, got: %q", stdout)
	}
}

func TestCLI_rm_nonexistent(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "rm", "ghost")
	if code == 0 {
		t.Error("rm of nonexistent server should exit non-zero")
	}
}

func TestCLI_add_invalidName(t *testing.T) {
	_, stderr, code := runCLI(t, t.TempDir(), "add", "bad/name", "--url", "http://example.com")
	if code == 0 || !strings.Contains(stderr, "invalid server name") {
		t.Errorf("expected non-zero exit + 'invalid server name', got code=%d stderr=%q", code, stderr)
	}
}

func TestCLI_add_noURLorCommand(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "add", "myserver")
	if code == 0 {
		t.Error("add with no URL or command should exit non-zero")
	}
}

func TestCLI_unknownCommand(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "boguscommand")
	if code != 2 {
		t.Errorf("unknown command should exit 2, got %d", code)
	}
}

func TestCLI_add_fromClaude(t *testing.T) {
	cfg := t.TempDir()
	claudeConfig := writeClaudeConfig(t, map[string]any{
		"command": "npx",
		"args":    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
	})
	_, _, code := runCLI(t, cfg, "add", "--from-claude", claudeConfig)
	if code != 0 {
		t.Fatalf("add --from-claude should exit 0, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(cfg, "servers", "imported-server.yaml")); err != nil {
		t.Errorf("expected imported-server.yaml to exist: %v", err)
	}
}

func TestCLI_init_createsDirectories(t *testing.T) {
	cfg := t.TempDir()
	_, _, code := runCLI(t, cfg, "init", "--yes")
	if code != 0 {
		t.Fatalf("init should exit 0, got %d", code)
	}
	for _, sub := range []string{"servers", "projections", "responses", "tokens"} {
		if _, err := os.Stat(filepath.Join(cfg, sub)); err != nil {
			t.Errorf("expected %s dir to exist after init: %v", sub, err)
		}
	}
}

func TestCLI_status_empty(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "status")
	if code != 0 {
		t.Errorf("status with no servers should exit 0, got %d", code)
	}
}


func TestCLI_connect_invalidConfig(t *testing.T) {
	cfg := t.TempDir()
	os.WriteFile(filepath.Join(cfg, "config.yaml"), []byte("not: valid: yaml: :::"), 0644)
	_, _, code := runCLI(t, cfg, "status")
	if code == 0 {
		t.Error("status with invalid config.yaml should exit non-zero")
	}
}

func TestCLI_add_protected(t *testing.T) {
	cfg := t.TempDir()
	_, _, code := runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp", "--protected", "list_items")
	if code != 0 {
		t.Fatalf("add --protected should exit 0, got %d", code)
	}
	data, err := os.ReadFile(filepath.Join(cfg, "servers", "myserver.yaml"))
	if err != nil {
		t.Fatalf("server YAML should exist: %v", err)
	}
	if !strings.Contains(string(data), "protected") {
		t.Errorf("server YAML should mention 'protected', got: %s", data)
	}
}

func writeClaudeCodeConfig(t *testing.T, servers map[string]any) string {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"projects": map[string]any{
			"/some/path": map[string]any{"mcpServers": servers},
		},
	})
	path := filepath.Join(t.TempDir(), "claude-code.json")
	os.WriteFile(path, data, 0644) //nolint:errcheck
	return path
}

func TestCLI_add_fromClaude_claudeCode(t *testing.T) {
	cfg := t.TempDir()
	path := writeClaudeCodeConfig(t, map[string]any{
		"code-server": map[string]any{
			"command": "npx",
			"args":    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
		},
	})
	_, _, code := runCLI(t, cfg, "add", "--from-claude", path)
	if code != 0 {
		t.Fatalf("add --from-claude (Claude Code format) should exit 0, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(cfg, "servers", "code-server.yaml")); err != nil {
		t.Errorf("expected code-server.yaml to exist: %v", err)
	}
}

func TestCLI_add_header(t *testing.T) {
	cfg := t.TempDir()
	_, _, code := runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp",
		"--header", "Authorization=Bearer tok", "--header", "X-Custom=val")
	if code != 0 {
		t.Fatalf("add --header should exit 0, got %d", code)
	}
	data, err := os.ReadFile(filepath.Join(cfg, "servers", "myserver.yaml"))
	if err != nil {
		t.Fatalf("server YAML should exist: %v", err)
	}
	if !strings.Contains(string(data), "Authorization") || !strings.Contains(string(data), "X-Custom") {
		t.Errorf("server YAML should contain headers, got: %s", data)
	}
}

func TestCLI_status_unreachable(t *testing.T) {
	cfg := t.TempDir()
	writeServerConfig(t, cfg, "bad", "name: bad\ncommand: /nonexistent_binary_xyz\n")
	_, _, code := runCLI(t, cfg, "status")
	if code == 0 {
		t.Error("status with unreachable server should exit non-zero")
	}
}

func TestCLI_init_fromPath(t *testing.T) {
	claudePath := writeClaudeConfig(t, map[string]any{
		"command": "npx",
		"args":    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
	})
	cfg := t.TempDir()
	_, _, code := runCLI(t, cfg, "init", "--yes", "--from", claudePath)
	if code != 0 {
		t.Fatalf("init --yes --from PATH should exit 0, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(cfg, "servers", "imported-server.yaml")); err != nil {
		t.Errorf("expected imported-server.yaml after init --from: %v", err)
	}
}

func TestCLI_cleanup_deletesExpiredFiles(t *testing.T) {
	cfg := t.TempDir()
	respDir := t.TempDir()
	writeConfig(t, cfg, "response_dir: "+respDir+"\nresponse_ttl: 1h\n")

	expiredPath := filepath.Join(respDir, "20200101000000000.json")
	os.WriteFile(expiredPath, []byte(`{}`), 0600)
	os.WriteFile(filepath.Join(respDir, "20200101000000000.raw.json"), []byte(`{}`), 0600)
	backdateFile(t, expiredPath, 2*time.Hour)

	stdout, _, code := runCLI(t, cfg, "cleanup")
	if code != 0 {
		t.Errorf("cleanup should exit 0, got %d", code)
	}
	if _, err := os.Stat(expiredPath); !os.IsNotExist(err) {
		t.Errorf("expired file should be deleted; stdout: %s", stdout)
	}
}

func TestCLI_cleanup_retainsNonExpiredFiles(t *testing.T) {
	cfg := t.TempDir()
	respDir := t.TempDir()
	writeConfig(t, cfg, "response_dir: "+respDir+"\nresponse_ttl: 1h\n")

	freshPath := filepath.Join(respDir, "20990101000000000.json")
	os.WriteFile(freshPath, []byte(`{}`), 0600)

	runCLI(t, cfg, "cleanup")
	if _, err := os.Stat(freshPath); err != nil {
		t.Error("non-expired file should not be deleted by cleanup")
	}
}

func TestCLI_cleanup_exits0(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "cleanup")
	if code != 0 {
		t.Errorf("cleanup with no responses dir should exit 0, got %d", code)
	}
}

func TestCLI_auth_serverNotFound(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "auth", "nonexistent")
	if code == 0 {
		t.Error("auth for nonexistent server should exit non-zero")
	}
}

func TestCLI_auth_noOAuth2Config(t *testing.T) {
	cfg := t.TempDir()
	runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp")
	_, stderr, code := runCLI(t, cfg, "auth", "myserver")
	if code == 0 {
		t.Error("auth for server without oauth2 config should exit non-zero")
	}
	if !strings.Contains(stderr, "oauth2") {
		t.Errorf("expected 'oauth2' in stderr, got: %s", stderr)
	}
}

func TestCLI_status_liveServer(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{
		"get_item":   `{"id":1}`,
		"list_items": `[]`,
	})
	writeServerYAML(t, cfg, "svc", dir, "")
	writeConfig(t, cfg, "inline_threshold: 50000\n")

	stdout, _, code := runCLI(t, cfg, "status")
	if code != 0 {
		t.Errorf("status with reachable server should exit 0, got %d\nstdout: %s", code, stdout)
	}
	if !strings.Contains(stdout, "svc") {
		t.Errorf("expected server name in status output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "ok") {
		t.Errorf("expected 'ok' status for live server, got: %s", stdout)
	}
}

func writeClaudeConfig(t *testing.T, serverDef any) string {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{"imported-server": serverDef},
	})
	path := filepath.Join(t.TempDir(), "claude.json")
	os.WriteFile(path, data, 0644)
	return path
}

