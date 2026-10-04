//go:build integration

package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/testutil"
)

func TestIntegrationCLIVersion(t *testing.T) {
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

func TestIntegrationCLIUnknownCommand(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "boguscommand")
	if code != 2 {
		t.Errorf("unknown command should exit 2, got %d", code)
	}
}

func TestIntegrationCLIRmMissingName_ExitsTwo(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "rm")
	if code != 2 {
		t.Errorf("rm with no NAME should exit 2, got %d", code)
	}
}

func TestIntegrationCLIAuthMissingName_ExitsTwo(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "auth")
	if code != 2 {
		t.Errorf("auth with no server name should exit 2, got %d", code)
	}
}

func TestIntegrationCLILsTooManyArgs_ExitsTwo(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "ls", "a", "b", "c")
	if code != 2 {
		t.Errorf("ls with 3 args should exit 2, got %d", code)
	}
}

func TestIntegrationCLIConnectInvalidConfig(t *testing.T) {
	cfg := t.TempDir()
	testutil.WriteFile(t, filepath.Join(cfg, "config.yaml"), "not: valid: yaml: :::")
	_, _, code := runCLI(t, cfg, "status")
	if code == 0 {
		t.Error("status with invalid config.yaml should exit non-zero")
	}
}

func TestIntegrationCLI_ls_Empty(t *testing.T) {
	stdout, _, code := runCLI(t, t.TempDir(), "ls")
	if code != 0 {
		t.Errorf("ls with empty config should exit 0, got %d", code)
	}
	if !strings.Contains(stdout, "no servers") {
		t.Errorf("expected 'no servers' in output, got: %q", stdout)
	}
}

func TestIntegrationCLI_ls_ServerListsTools(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{
		"get_item":   `{"id":1}`,
		"list_items": `[]`,
	})
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})

	stdout, _, code := runCLI(t, cfg, "ls", "svc")
	if code != 0 {
		t.Fatalf("ls <server> should exit 0, got %d\nstdout: %s", code, stdout)
	}
	if !strings.Contains(stdout, "get_item") {
		t.Errorf("expected tool name in output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "TOOL") {
		t.Errorf("expected TOOL header in output, got: %s", stdout)
	}
}

func TestIntegrationCLI_aBrokenServerFileOnlyAffectsThatServer(t *testing.T) {
	cfg := t.TempDir()
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})})
	testutil.WriteFile(t, config.ServerPath(cfg, "broken"), "command: [unclosed\n")

	t.Run("ls warns about it and lists the rest", func(t *testing.T) {
		stdout, stderr, code := runCLI(t, cfg, "ls")
		if code != 0 || !strings.Contains(stdout, "svc") || !strings.Contains(stderr, "skipping server broken") {
			t.Errorf("ls = exit %d, stdout %q, stderr %q; want svc listed and broken warned about", code, stdout, stderr)
		}
	})
	t.Run("another server still lists its tools", func(t *testing.T) {
		stdout, stderr, code := runCLI(t, cfg, "ls", "svc")
		if code != 0 || !strings.Contains(stdout, "get_item") {
			t.Errorf("ls svc = exit %d, stdout %q, stderr %q; want svc's tools", code, stdout, stderr)
		}
	})
	for _, command := range []string{"status", "test"} {
		t.Run(command+" checks the rest and fails for it", func(t *testing.T) {
			stdout, _, code := runCLI(t, cfg, command)
			if code == 0 || !strings.Contains(stdout, "broken") || !strings.Contains(stdout, "svc") {
				t.Errorf("%s = exit %d, stdout %q; want non-zero, with a row for broken and svc", command, code, stdout)
			}
		})
	}
}

func TestIntegrationCLI_test_reportsEachServerOnceWhateverFailedToLoad(t *testing.T) {
	cfg := t.TempDir()
	fixtures := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: fixtures})
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "unprojected", Fixtures: fixtures})
	testutil.WriteFile(t, config.ProjectionPath(cfg, "unprojected"), "get_item: [broken\n")
	testutil.WriteFile(t, config.ServerPath(cfg, "web"), "transport: http\nurl: https://example.com/mcp\nhandshake_timeout: nonsense\n")
	testutil.WriteFile(t, config.ServerPath(cfg, "typo"), "command: echo\nenabled: maybe\n") // yaml lists type errors on lines of their own

	stdout, _, code := runCLI(t, cfg, "test")

	if code == 0 || !strings.Contains(stdout, "1 passed, 3 failed") {
		t.Errorf("test = exit %d, stdout %q; want 1 passed, 3 failed", code, stdout)
	}
	for line := range strings.Lines(strings.TrimSpace(stdout)) {
		if !strings.HasPrefix(line, "PASS") && !strings.HasPrefix(line, "FAIL") && strings.TrimSpace(line) != "" && !strings.Contains(line, "passed") {
			t.Errorf("test printed %q outside any row; a multi-line error broke its row apart", line)
		}
	}
	if rows := testRowsFor(stdout, "unprojected"); len(rows) != 1 || rows[0][0] != "FAIL" {
		t.Errorf("unprojected rows = %q; want one FAIL row, for its projections", rows)
	}
	if rows := testRowsFor(stdout, "web"); len(rows) != 1 || rows[0][2] == "stdio" {
		t.Errorf("web rows = %q; its file didn't load, so its transport isn't known", rows)
	}
}

func testRowsFor(stdout, server string) [][]string {
	var rows [][]string
	for line := range strings.Lines(stdout) {
		if fields := strings.Fields(line); len(fields) > 2 && fields[1] == server {
			rows = append(rows, fields)
		}
	}
	return rows
}

func TestIntegrationCLI_test_failsForBrokenProjectionsEvenWithNoServerToCheck(t *testing.T) {
	cfg := t.TempDir()
	disabled := false
	configtest.WriteServer(t, cfg, config.ServerConfig{Name: "off", Command: "echo", Enabled: &disabled})
	testutil.WriteFile(t, config.ProjectionPath(cfg, "off"), "tool: [broken\n")

	stdout, _, code := runCLI(t, cfg, "test")

	if code == 0 || !strings.Contains(stdout, "off") {
		t.Errorf("test = exit %d, stdout %q; want non-zero with a row for off's projections", code, stdout)
	}
}

func TestIntegrationCLI_ls_ToolDetail(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{
		"get_item":   `{"id":1}`,
		"list_items": `[]`,
	})
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})

	stdout, _, code := runCLI(t, cfg, "ls", "svc", "get_item")
	if code != 0 {
		t.Fatalf("ls <server> <tool> should exit 0, got %d\nstdout: %s", code, stdout)
	}
	if !strings.Contains(stdout, "get_item") {
		t.Errorf("expected tool name in detail output, got: %s", stdout)
	}
}

func TestIntegrationCLI_ls_UnknownServer(t *testing.T) {
	_, stderr, code := runCLI(t, t.TempDir(), "ls", "ghost")
	if code == 0 {
		t.Error("ls with unknown server should exit non-zero")
	}
	if !strings.Contains(stderr, "ghost") {
		t.Errorf("expected server name in error output, got: %q", stderr)
	}
}

func TestIntegrationCLI_ls_UnknownTool(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{"get_item": `{"id":1}`})
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})

	_, stderr, code := runCLI(t, cfg, "ls", "svc", "nonexistent_tool")
	if code == 0 {
		t.Error("ls with unknown tool should exit non-zero")
	}
	if !strings.Contains(stderr, "nonexistent_tool") {
		t.Errorf("expected tool name in error output, got: %q", stderr)
	}
}

func TestIntegrationCLI_add_ThenLs(t *testing.T) {
	cfg := t.TempDir()
	runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp", "--no-connect")
	stdout, _, code := runCLI(t, cfg, "ls")
	if code != 0 || !strings.Contains(stdout, "myserver") {
		t.Errorf("ls after add: code=%d, output=%q", code, stdout)
	}
}

func TestIntegrationCLI_add_UrlCreatesFile(t *testing.T) {
	cfg := t.TempDir()
	_, _, code := runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp", "--no-connect")
	if code != 0 {
		t.Fatalf("add should exit 0, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(cfg, "servers", "myserver.yaml")); err != nil {
		t.Errorf("expected servers/myserver.yaml to exist: %v", err)
	}
}

func TestIntegrationCLI_add_CommandCreatesFile(t *testing.T) {
	cfg := t.TempDir()
	_, _, code := runCLI(t, cfg, "add", "myserver", "--", "npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp")
	if code != 0 {
		t.Fatalf("add with command should exit 0, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(cfg, "servers", "myserver.yaml")); err != nil {
		t.Errorf("expected servers/myserver.yaml to exist: %v", err)
	}
}

func TestIntegrationCLI_add_InvalidName(t *testing.T) {
	_, stderr, code := runCLI(t, t.TempDir(), "add", "bad/name", "--url", "http://example.com", "--no-connect")
	if code == 0 || !strings.Contains(stderr, "invalid server name") {
		t.Errorf("expected non-zero exit + 'invalid server name', got code=%d stderr=%q", code, stderr)
	}
}

func TestIntegrationCLI_add_NoURLOrCommand(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "add", "myserver")
	if code == 0 {
		t.Error("add with no URL or command should exit non-zero")
	}
}

func TestIntegrationCLI_add_Protected(t *testing.T) {
	cfg := t.TempDir()
	_, _, code := runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp", "--protected", "list_items", "--no-connect")
	if code != 0 {
		t.Fatalf("add --protected should exit 0, got %d", code)
	}
	data := testutil.ReadFile(t, filepath.Join(cfg, "servers", "myserver.yaml"))
	if !strings.Contains(string(data), "protected") {
		t.Errorf("server YAML should mention 'protected', got: %s", data)
	}
}

func TestIntegrationCLI_add_Header(t *testing.T) {
	cfg := t.TempDir()
	_, _, code := runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp",
		"--header", "Authorization=Bearer tok", "--header", "X-Custom=val", "--no-connect")
	if code != 0 {
		t.Fatalf("add --header should exit 0, got %d", code)
	}
	data := testutil.ReadFile(t, filepath.Join(cfg, "servers", "myserver.yaml"))
	if !strings.Contains(string(data), "Authorization") || !strings.Contains(string(data), "X-Custom") {
		t.Errorf("server YAML should contain headers, got: %s", data)
	}
}

func TestIntegrationCLI_add_FromClaude(t *testing.T) {
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

func TestIntegrationCLI_add_FromClaudeCode(t *testing.T) {
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

func TestIntegrationCLI_configFlagMayFollowSubcommand(t *testing.T) {
	cfg := t.TempDir()
	runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp", "--no-connect")

	cmd := exec.Command(miniBin, "rm", "myserver", "--config", cfg)
	var errOut strings.Builder
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("rm with trailing --config should exit 0: %v (stderr=%s)", err, errOut.String())
	}
	stdout, _, code := runCLI(t, cfg, "ls")
	if code != 0 || strings.Contains(stdout, "myserver") {
		t.Errorf("leading --config did not select %s: code=%d ls=%q", cfg, code, stdout)
	}
}

func TestIntegrationCLI_addHelp_ShowsFlags(t *testing.T) {
	cmd := exec.Command(miniBin, "add", "--help")
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("add --help should exit 0: %v", err)
	}
	if !strings.Contains(out.String(), "--url") {
		t.Errorf("expected --url in `add --help` output, got: %q", out.String())
	}
}

func TestIntegrationCLI_rm_Server(t *testing.T) {
	cfg := t.TempDir()
	runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp", "--no-connect")
	_, _, code := runCLI(t, cfg, "rm", "myserver")
	if code != 0 {
		t.Errorf("rm should exit 0, got %d", code)
	}
	stdout, _, _ := runCLI(t, cfg, "ls")
	if strings.Contains(stdout, "myserver") {
		t.Errorf("myserver should not appear in ls after rm, got: %q", stdout)
	}
}

func TestIntegrationCLI_rm_Nonexistent(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "rm", "ghost")
	if code == 0 {
		t.Error("rm of nonexistent server should exit non-zero")
	}
}

func TestIntegrationCLI_status_Empty(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "status")
	if code != 0 {
		t.Errorf("status with no servers should exit 0, got %d", code)
	}
}

func TestIntegrationCLI_status_LiveServer(t *testing.T) {
	cfg := t.TempDir()
	dir := mockFixtureDir(t, map[string]string{
		"get_item":   `{"id":1}`,
		"list_items": `[]`,
	})
	writeFakeServer(t, cfg, fakeServerParams{ServerName: "svc", Fixtures: dir})

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

func TestIntegrationCLI_status_Unreachable(t *testing.T) {
	cfg := t.TempDir()
	configtest.WriteServer(t, cfg, config.ServerConfig{Name: "bad", Command: "/nonexistent_binary_xyz"})
	_, _, code := runCLI(t, cfg, "status")
	if code == 0 {
		t.Error("status with unreachable server should exit non-zero")
	}
}

func TestIntegrationCLI_init_CreatesDirectories(t *testing.T) {
	cfg := t.TempDir()
	_, _, code := runCLI(t, cfg, "init", "--yes")
	if code != 0 {
		t.Fatalf("init should exit 0, got %d", code)
	}
	for _, sub := range []string{"servers", "internal", "internal/responses"} {
		if _, err := os.Stat(filepath.Join(cfg, sub)); err != nil {
			t.Errorf("expected %s dir to exist after init: %v", sub, err)
		}
	}
}

func TestIntegrationCLI_init_FromPath(t *testing.T) {
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

func TestIntegrationCLI_init_yesWithOAuthServer_listsLoginReminderBeforeInstallInstructions(t *testing.T) {
	claudePath := writeClaudeConfig(t, map[string]any{"type": "http", "url": "https://slack.com/mcp"})
	cfg := t.TempDir()
	stdout, _, code := runCLI(t, cfg, "init", "--yes", "--from", claudePath)
	if code != 0 {
		t.Fatalf("init exit = %d, stdout:\n%s", code, stdout)
	}
	markers := []string{"imported 1 server(s)", "OAuth login needed:", "imported-server (no token)", "  mini auth imported-server\n", "To connect mini to your agent"}
	last := -1
	for _, marker := range markers {
		i := strings.Index(stdout, marker)
		if i < 0 {
			t.Fatalf("stdout missing %q:\n%s", marker, stdout)
		}
		if i < last {
			t.Errorf("%q appears before the previous marker:\n%s", marker, stdout)
		}
		last = i
	}
}

func TestIntegrationCLI_cleanup_DeletesExpiredFiles(t *testing.T) {
	cfg := t.TempDir()
	respDir := t.TempDir()
	fixtureConfig := config.DefaultConfig()
	fixtureConfig.ResponseDir = respDir
	fixtureConfig.ResponseTTL = "1h"
	configtest.WriteConfig(t, cfg, fixtureConfig)

	expiredPath := filepath.Join(respDir, "20200101000000000.json")
	testutil.WriteFile(t, expiredPath, `{}`)
	testutil.WriteFile(t, filepath.Join(respDir, "20200101000000000.raw.json"), `{}`)
	backdateFile(t, expiredPath, 2*time.Hour)

	stdout, _, code := runCLI(t, cfg, "cleanup")
	if code != 0 {
		t.Errorf("cleanup should exit 0, got %d", code)
	}
	if _, err := os.Stat(expiredPath); !os.IsNotExist(err) {
		t.Errorf("expired file should be deleted; stdout: %s", stdout)
	}
}

func TestIntegrationCLI_cleanup_RetainsNonExpiredFiles(t *testing.T) {
	cfg := t.TempDir()
	respDir := t.TempDir()
	fixtureConfig := config.DefaultConfig()
	fixtureConfig.ResponseDir = respDir
	fixtureConfig.ResponseTTL = "1h"
	configtest.WriteConfig(t, cfg, fixtureConfig)

	freshPath := filepath.Join(respDir, "20990101000000000.json")
	testutil.WriteFile(t, freshPath, `{}`)

	runCLI(t, cfg, "cleanup")
	if _, err := os.Stat(freshPath); err != nil {
		t.Error("non-expired file should not be deleted by cleanup")
	}
}

func TestIntegrationCLI_cleanup_Exits0(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "cleanup")
	if code != 0 {
		t.Errorf("cleanup with no responses dir should exit 0, got %d", code)
	}
}

func TestIntegrationCLI_auth_ServerNotFound(t *testing.T) {
	_, _, code := runCLI(t, t.TempDir(), "auth", "nonexistent")
	if code == 0 {
		t.Error("auth for nonexistent server should exit non-zero")
	}
}

func TestIntegrationCLI_auth_NoOAuth2Config(t *testing.T) {
	cfg := t.TempDir()
	runCLI(t, cfg, "add", "myserver", "--url", "http://example.com/mcp", "--no-connect")
	_, stderr, code := runCLI(t, cfg, "auth", "myserver")
	if code == 0 {
		t.Error("auth for server without oauth2 config should exit non-zero")
	}
	if !strings.Contains(stderr, "oauth2") {
		t.Errorf("expected 'oauth2' in stderr, got: %s", stderr)
	}
}

func TestIntegrationCLI_add_DetectsOAuthAndStartsAuthorization(t *testing.T) {
	unauthorized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer unauthorized.Close()

	cfg := t.TempDir()
	fixtureConfig := config.DefaultConfig()
	fixtureConfig.DisableAuthBrowserOpen = true
	configtest.WriteConfig(t, cfg, fixtureConfig)

	stdout, _, code := runCLI(t, cfg, "add", "myserver", "--url", unauthorized.URL)
	if code != 0 {
		t.Errorf("expected exit 0 even though auto-authorization can't complete against a loopback test server, got %d", code)
	}
	if !strings.Contains(stdout, "requires OAuth authorization") {
		t.Errorf("expected stdout to mention required OAuth authorization, got: %q", stdout)
	}
	if !strings.Contains(stdout, "run `mini auth myserver` to retry") {
		t.Errorf("expected stdout to point at a manual retry after the automatic flow fails, got: %q", stdout)
	}

	if _, err := os.Stat(filepath.Join(cfg, "servers", "myserver.yaml")); err != nil {
		t.Fatalf("server YAML should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "internal", "myserver.meta.json")); err != nil {
		t.Errorf("expected the oauth-detected marker to be written, got: %v", err)
	}
}

func writeClaudeConfig(t *testing.T, serverDef any) string {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{"imported-server": serverDef},
	})
	path := filepath.Join(t.TempDir(), "claude.json")
	testutil.WriteFileBytes(t, path, data)
	return path
}

func writeClaudeCodeConfig(t *testing.T, servers map[string]any) string {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"projects": map[string]any{
			"/some/path": map[string]any{"mcpServers": servers},
		},
	})
	path := filepath.Join(t.TempDir(), "claude-code.json")
	testutil.WriteFileBytes(t, path, data)
	return path
}
