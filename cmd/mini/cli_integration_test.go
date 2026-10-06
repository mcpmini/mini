//go:build integration

package main_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/testutil"
)

func miniBin(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("MINIMCP_BIN")
	if bin == "" {
		// Try building it on the fly if running from the source tree.
		tmp := t.TempDir()
		out := filepath.Join(tmp, "mini")
		cmd := exec.Command("go", "build", "-o", out, ".")
		cmd.Dir = "."
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("could not build mini: %v\n%s", err, b)
		}
		return out
	}
	return bin
}

func run(t *testing.T, bin, configDir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	a := append([]string{"--config", configDir}, args...)
	cmd := exec.Command(bin, a...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()
	code = 0
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	return
}

func TestIntegrationCLI_ls_empty(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	stdout, _, code := run(t, bin, cfg, "ls")
	if code != 0 {
		t.Errorf("ls with empty config should exit 0, got %d", code)
	}
	if !strings.Contains(stdout, "no servers") {
		t.Errorf("expected 'no servers' in output, got: %q", stdout)
	}
}

func TestIntegrationCLI_add_and_ls(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()

	_, _, code := run(t, bin, cfg, "add", "myserver", "--url", "http://localhost:9999")
	if code != 0 {
		t.Fatalf("add should exit 0, got %d", code)
	}
	serverFile := filepath.Join(cfg, "servers", "myserver.yaml")
	if _, err := os.Stat(serverFile); err != nil {
		t.Errorf("expected servers/myserver.yaml to exist: %v", err)
	}
	checkLsContains(t, bin, cfg, "myserver", "http")
}

func TestIntegrationCLI_add_preservesChildFlags(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	cmd := exec.Command(bin, "add", "svc", "--config", cfg, "--", "/usr/bin/printf", "-h", "--config", "child-value")
	output, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run add: %v", err)
	}
	if code != 0 {
		t.Fatalf("add exited %d: %s", code, output)
	}
	data := testutil.ReadFile(t, filepath.Join(cfg, "servers", "svc.yaml"))
	for _, want := range []string{"- -h", "- --config", "- child-value"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("server config %q does not contain %q", data, want)
		}
	}
}

func TestIntegrationCLI_configAfterSubcommandIsAccepted(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	stdout, stderr, code := run(t, bin, t.TempDir(), "ls", "--config", cfg)
	if code != 0 || !strings.Contains(stdout, "no servers") {
		t.Fatalf("ls exited %d: stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestIntegrationCLI_addRequiresDashBeforeStdioCommand(t *testing.T) {
	bin := miniBin(t)
	_, stderr, code := run(t, bin, t.TempDir(), "add", "svc", "printf", "hello")
	if code != 2 || !strings.Contains(stderr, "require NAME -- CMD") {
		t.Fatalf("add exited %d: %s", code, stderr)
	}
}

func checkLsContains(t *testing.T, bin, cfg string, want ...string) {
	t.Helper()
	stdout, _, code := run(t, bin, cfg, "ls")
	if code != 0 {
		t.Errorf("ls should exit 0, got %d", code)
	}
	for _, s := range want {
		if !strings.Contains(stdout, s) {
			t.Errorf("expected %q in ls output, got: %q", s, stdout)
		}
	}
}

func TestIntegrationCLI_rm(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()

	run(t, bin, cfg, "add", "myserver", "--url", "http://localhost:9999")

	_, _, code := run(t, bin, cfg, "rm", "myserver")
	if code != 0 {
		t.Errorf("rm should exit 0, got %d", code)
	}

	stdout, _, _ := run(t, bin, cfg, "ls")
	if strings.Contains(stdout, "myserver") {
		t.Errorf("myserver should not appear in ls after rm, got: %q", stdout)
	}
}

func TestIntegrationCLI_rm_nonexistent(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	_, _, code := run(t, bin, cfg, "rm", "ghost")
	if code == 0 {
		t.Error("rm of nonexistent server should exit non-zero")
	}
}

func TestIntegrationCLI_add_invalidName(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	_, stderr, code := run(t, bin, cfg, "add", "bad/name", "--url", "http://localhost:9999")
	if code == 0 {
		t.Error("add with invalid name should exit non-zero")
	}
	if !strings.Contains(stderr, "invalid server name") {
		t.Errorf("expected 'invalid server name' in stderr, got: %q", stderr)
	}
}

func TestIntegrationCLI_unknownCommand(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	_, _, code := run(t, bin, cfg, "boguscommand")
	if code != 2 {
		t.Errorf("unknown command should exit 2, got %d", code)
	}
}

func TestIntegrationCLI_rm_missingName_exitsTwo(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	_, _, code := run(t, bin, cfg, "rm")
	if code != 2 {
		t.Errorf("rm with no NAME should exit 2, got %d", code)
	}
}

func TestIntegrationCLI_auth_missingName_exitsTwo(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	_, _, code := run(t, bin, cfg, "auth")
	if code != 2 {
		t.Errorf("auth with no server name should exit 2, got %d", code)
	}
}

func TestIntegrationCLI_ls_tooManyArgs_exitsTwo(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	_, _, code := run(t, bin, cfg, "ls", "a", "b", "c")
	if code != 2 {
		t.Errorf("ls with 3 args should exit 2, got %d", code)
	}
}

func TestIntegrationCLI_test_noServers(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	_, _, code := run(t, bin, cfg, "test")
	if code != 0 {
		t.Errorf("test with no servers should exit 0, got %d", code)
	}
}

func TestIntegrationCLI_test_unreachableServer(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()

	run(t, bin, cfg, "add", "dead", "--url", "http://127.0.0.1:19999")

	stdout, _, code := run(t, bin, cfg, "test", "--timeout", "2s")
	if code == 0 {
		t.Error("test with unreachable server should exit non-zero")
	}
	if !strings.Contains(stdout, "FAIL") {
		t.Errorf("expected FAIL in output, got: %q", stdout)
	}
}

func TestIntegrationCLI_init_createsStructure(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()

	stdout, _, code := run(t, bin, cfg, "init", "--yes")
	if code != 0 {
		t.Errorf("init should exit 0, got %d", code)
	}

	for _, sub := range []string{"servers", "internal", "internal/responses"} {
		d := filepath.Join(cfg, sub)
		if _, err := os.Stat(d); err != nil {
			t.Errorf("expected %s dir to exist after init: %v", sub, err)
		}
	}

	if !strings.Contains(stdout, `"connect"`) {
		t.Errorf("init output should include connect arg in install snippet, got: %q", stdout)
	}
}

func writeClaudeConfigFile(t *testing.T) string {
	t.Helper()
	claudeConfig := filepath.Join(t.TempDir(), "claude.json")
	claudeData, _ := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"imported-server": map[string]any{
				"command": "npx",
				"args":    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			},
		},
	})
	testutil.WriteFileBytes(t, claudeConfig, claudeData)
	return claudeConfig
}

func TestIntegrationCLI_init_importFromClaude(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	claudeConfig := writeClaudeConfigFile(t)
	_, _, code := run(t, bin, cfg, "init", "--yes", "--from", claudeConfig)
	if code != 0 {
		t.Errorf("init --from should exit 0, got %d", code)
	}
	serverFile := filepath.Join(cfg, "servers", "imported-server.yaml")
	if _, err := os.Stat(serverFile); err != nil {
		t.Errorf("expected imported-server.yaml to exist: %v", err)
	}
}

func TestIntegrationCLI_connect_badConfig(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()

	testutil.WriteFile(t, filepath.Join(cfg, "config.yaml"), "not: valid: yaml: :::")

	_, _, code := run(t, bin, cfg, "connect")
	if code == 0 {
		t.Error("connect with invalid config.yaml should exit non-zero")
	}
}

func TestIntegrationCLI_bareMini_printsHelpAndExitsZero(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	stdout, _, code := run(t, bin, cfg)
	if code != 0 {
		t.Errorf("bare mini should exit 0, got %d", code)
	}
	if !strings.Contains(stdout, "connect") {
		t.Errorf("bare mini should print help mentioning connect, got: %q", stdout)
	}
}

func TestIntegrationCLI_connect_invalidToolMode(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	_, stderr, code := run(t, bin, cfg, "connect", "--tool-mode", "bogus")
	if code == 0 {
		t.Error("connect with invalid --tool-mode should exit non-zero")
	}
	if !strings.Contains(stderr, "tool-mode") {
		t.Errorf("expected --tool-mode validation error in stderr, got: %q", stderr)
	}
}

func TestIntegrationCLI_cleanup_empty(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	_, _, code := run(t, bin, cfg, "cleanup")
	if code != 0 {
		t.Errorf("cleanup with no response files should exit 0, got %d", code)
	}
}

func TestIntegrationCLI_daemon_status_noDaemon(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	stdout, _, code := run(t, bin, cfg, "daemon", "status")
	if code != 0 {
		t.Errorf("daemon status should exit 0 even without running daemon, got %d", code)
	}
	if !strings.Contains(stdout, "not running") && !strings.Contains(stdout, "no daemon") {
		t.Errorf("expected 'not running' or similar in output, got: %q", stdout)
	}
}

func TestIntegrationCLI_add_withHeader(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	_, _, code := run(
		t,
		bin,
		cfg,
		"add",
		"myserver",
		"--url",
		"http://example.com/mcp",
		"--header",
		"Authorization=Bearer tok123",
	)
	if code != 0 {
		t.Fatalf("add --header should exit 0, got %d", code)
	}
	data := testutil.ReadFile(t, filepath.Join(cfg, "servers", "myserver.yaml"))
	if !strings.Contains(string(data), "Authorization") {
		t.Errorf("server YAML should contain Authorization header, got: %s", data)
	}
}

func TestIntegrationCLI_add_stdioCommand(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	_, _, code := run(
		t,
		bin,
		cfg,
		"add",
		"localserver",
		"--no-connect",
		"--",
		"npx",
		"-y",
		"@modelcontextprotocol/server-filesystem",
		"/tmp",
	)
	if code != 0 {
		t.Fatalf("add stdio command should exit 0, got %d", code)
	}
	data := testutil.ReadFile(t, filepath.Join(cfg, "servers", "localserver.yaml"))
	if !strings.Contains(string(data), "command") {
		t.Errorf("server YAML should contain command field, got: %s", data)
	}
}

// versionPattern matches valid outputs from internal/version.computeVersion:
//   - "a1b2c3d"            — 7-char hex hash, clean tree
//   - "a1b2c3d+dirty"      — hash, dirty tree
//   - "v1.2.3 (a1b2c3d)"  — release tag with hash
//   - "dev"                — no VCS info, which is what builds from a git worktree get
var versionPattern = regexp.MustCompile(`^(dev|[0-9a-f]{7}(\+dirty)?|v[0-9]+\.[0-9]+\.[0-9]+[^ ]* \([0-9a-f]{7}\))$`)

func TestIntegrationCLI_version(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	stdout, _, code := run(t, bin, cfg, "version")
	if code != 0 {
		t.Fatalf("version should exit 0, got %d", code)
	}
	if !versionPattern.MatchString(strings.TrimSpace(stdout)) {
		t.Errorf("unexpected version output %q", stdout)
	}
}

func TestIntegrationCLI_version_flag(t *testing.T) {
	bin := miniBin(t)
	// --version is a global flag, not a subcommand — run without --config
	cmd := exec.Command(bin, "--version")
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("--version should exit 0: %v", err)
	}
	if !versionPattern.MatchString(strings.TrimSpace(out.String())) {
		t.Errorf("unexpected version output %q", out.String())
	}
}

func TestIntegrationCLI_add_protectedTool(t *testing.T) {
	bin := miniBin(t)
	cfg := t.TempDir()
	_, _, code := run(
		t,
		bin,
		cfg,
		"add",
		"svc",
		"--url",
		"http://example.com/mcp",
		"--protected",
		"delete_item",
		"--protected",
		"create_item",
	)
	if code != 0 {
		t.Fatalf("add --protected should exit 0, got %d", code)
	}
	data := testutil.ReadFile(t, filepath.Join(cfg, "servers", "svc.yaml"))
	yaml := string(data)
	if !strings.Contains(yaml, "delete_item") || !strings.Contains(yaml, "create_item") {
		t.Errorf("YAML should contain both protected tools, got: %s", yaml)
	}
}
