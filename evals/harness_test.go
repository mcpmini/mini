//go:build evals

// Run: go test -tags evals ./evals/... -v -timeout 300s
package evals_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var (
	miniBin     string
	fakemcpBin  string
	fixturesDir string
	repoRoot    string
)

func TestMain(m *testing.M) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot = filepath.Join(filepath.Dir(thisFile), "..")
	fixturesDir = filepath.Join(repoRoot, "benchmarks", "fixtures")
	miniBin = mustBuildEvalBinary(repoRoot, "mini", "./cmd/mini")
	fakemcpBin = mustBuildEvalBinary(repoRoot, "fakemcp", "./test/fakemcp", "-tags", "integration")
	os.Exit(m.Run())
}

func buildBin(root, name, pkg string, extraFlags ...string) (string, error) {
	tmp, err := os.MkdirTemp("", "mini-evals-*")
	if err != nil {
		return "", err
	}
	out := filepath.Join(tmp, name)
	args := []string{"build"}
	args = append(args, extraFlags...)
	args = append(args, "-o", out, pkg)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v\n%s", err, b)
	}
	return out, nil
}

func mustBuildEvalBinary(root, name, pkg string, extraFlags ...string) string {
	out, err := buildBin(root, name, pkg, extraFlags...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build", name+":", err)
		os.Exit(1)
	}
	return out
}

// ClaudeResult holds outcome and token usage for a single Claude run.
type ClaudeResult struct {
	Text             string
	InputTokens      int
	CacheReadTokens  int
	CacheWriteTokens int
	OutputTokens     int
	TotalCostUSD     float64
	Turns            int
	WorkDir          string
	CallLogDir       string
	RawOutputPath    string
}

func (r ClaudeResult) EffectiveInputTokens() int {
	return r.InputTokens + r.CacheReadTokens + r.CacheWriteTokens
}

// Output format configs for mini (MCP and CLI modes).
const (
	fmtPassthrough = iota // no projection, full JSON inline — mini as thin proxy
	fmtProjected          // bundled default projections, JSON envelope
	fmtLines              // bundled default projections, lines/mini format
	numFormats     = 3
)

var fmtLabel = [numFormats]string{"passthrough", "projected", "lines"}

// EvalResult holds results for all 7 runs: 1 raw baseline + 3 MCP + 3 CLI.
type EvalResult struct {
	Raw ClaudeResult            // Claude direct to upstreams, no mini
	MCP [numFormats]ClaudeResult // Claude → mini MCP server, 3 format configs
	CLI [numFormats]ClaudeResult // Claude → mini call (bash), 3 format configs
}

func logEval(t *testing.T, label string, r EvalResult) {
	t.Helper()
	rawEff := r.Raw.EffectiveInputTokens()
	pct := func(eff int) string {
		if rawEff == 0 {
			return "    n/a"
		}
		return fmt.Sprintf("%+6.1f%%", float64(eff-rawEff)/float64(rawEff)*100)
	}
	row := func(mode string, c ClaudeResult) {
		t.Logf("  %-22s %6d in + %4d out  $%.4f  (%d turns)  %s",
			mode,
			c.EffectiveInputTokens(), c.OutputTokens,
			c.TotalCostUSD, c.Turns,
			pct(c.EffectiveInputTokens()))
	}
	t.Logf("\n╔══ Token Report: %s ══╗", label)
	t.Logf("  %-22s %6s    %4s   %7s  %8s  %s", "Mode", "In", "Out", "Cost", "Turns", "vs Raw")
	row("raw (baseline)", r.Raw)
	for i := range numFormats {
		row("mcp-"+fmtLabel[i], r.MCP[i])
		row("cli-"+fmtLabel[i], r.CLI[i])
	}
	t.Logf("╚══════════════════════════════════════╝")
	t.Logf("[raw]              %s", r.Raw.Text)
	for i := range numFormats {
		t.Logf("[mcp-%s] %s", fmtLabel[i], r.MCP[i].Text)
		t.Logf("[cli-%s] %s", fmtLabel[i], r.CLI[i].Text)
	}
}

type evalParams struct {
	servers      map[string]string
	allowedTools string
	workSrcDir   string
}

// runSetup holds pre-created paths for a single Claude run.
type runSetup struct {
	kind    string // "raw", "mcp", "cli"
	idx     int
	cmd     func() *exec.Cmd
	workDir string
	callDir string
}

// runEval launches all 7 Claude runs in parallel and collects results.
// All directories and configs are created on the test goroutine before
// launching goroutines (required because t.Fatal is not safe from goroutines).
func runEval(t *testing.T, p evalParams, task string) EvalResult {
	t.Helper()

	rawCallDir := preservedDir(t, "raw")
	rawCfg := rawMCPConfig(t, p.servers, rawCallDir)
	rawWorkDir := freshWorkDir(t, p.workSrcDir)

	setups := []runSetup{
		{
			kind:    "raw",
			idx:     0,
			workDir: rawWorkDir,
			callDir: rawCallDir,
			cmd: func() *exec.Cmd {
				return buildClaudeCmd(rawCfg, rawAllowedTools(p.servers, p.allowedTools), rawWorkDir, task)
			},
		},
	}

	for i := range numFormats {
		mcpCallDir := preservedDir(t, "mcp-"+fmtLabel[i])
		cliCallDir := preservedDir(t, "cli-"+fmtLabel[i])
		mcpCfg := miniMCPConfig(t, p.servers, mcpCallDir, i)
		cliCfgDir := miniCLIConfigDir(t, p.servers, cliCallDir, i)
		wrapDir := writeMiniWrapper(t, cliCfgDir)
		mcpWork := freshWorkDir(t, p.workSrcDir)
		cliWork := freshWorkDir(t, p.workSrcDir)

		mcpAllowed := miniMCPAllowedTools(p.allowedTools)
		cliAllowed := cliAllowedTools(p.allowedTools)

		// Capture loop vars for closures.
		mCfg, mWork, mCallDir := mcpCfg, mcpWork, mcpCallDir
		cWrap, cWork, cCallDir := wrapDir, cliWork, cliCallDir

		setups = append(setups,
			runSetup{
				kind: "mcp", idx: i,
				workDir: mWork, callDir: mCallDir,
				cmd: func() *exec.Cmd {
					return buildClaudeCmd(mCfg, mcpAllowed, mWork, task)
				},
			},
			runSetup{
				kind: "cli", idx: i,
				workDir: cWork, callDir: cCallDir,
				cmd: func() *exec.Cmd {
					return buildClaudeCLICmd(cWrap, cliAllowed, cWork, task)
				},
			},
		)
	}

	type jobResult struct {
		kind string
		idx  int
		r    ClaudeResult
		err  error
	}

	ch := make(chan jobResult, len(setups))
	for _, s := range setups {
		s := s
		go func() {
			output, err := runClaudeCmd(s.cmd(), s.callDir)
			result := parseClaudeResult(output)
			result.WorkDir = s.workDir
			result.CallLogDir = s.callDir
			result.RawOutputPath = saveOutput(s.callDir, "claude-output.json", output)
			ch <- jobResult{s.kind, s.idx, result, err}
		}()
	}

	var eval EvalResult
	for range len(setups) {
		res := <-ch
		label := res.kind
		if res.kind != "raw" {
			label += "-" + fmtLabel[res.idx]
		}
		if res.err != nil {
			t.Errorf("[%s] run failed: %v", label, res.err)
		}
		switch res.kind {
		case "raw":
			eval.Raw = res.r
		case "mcp":
			eval.MCP[res.idx] = res.r
		case "cli":
			eval.CLI[res.idx] = res.r
		}
	}
	return eval
}

// evalWithLabels returns all 7 runs for range-based assertion iteration.
func evalWithLabels(r EvalResult) []struct {
	label  string
	result ClaudeResult
} {
	out := []struct {
		label  string
		result ClaudeResult
	}{{"raw", r.Raw}}
	for i := range numFormats {
		out = append(out, struct {
			label  string
			result ClaudeResult
		}{"mcp-" + fmtLabel[i], r.MCP[i]})
		out = append(out, struct {
			label  string
			result ClaudeResult
		}{"cli-" + fmtLabel[i], r.CLI[i]})
	}
	return out
}

// rawAllowedTools builds the explicit tool allowlist for raw mode.
// Claude's --allowedTools requires exact tool names; globs are not supported.
func rawAllowedTools(servers map[string]string, extraBuiltins string) string {
	var names []string
	for serverName, dir := range servers {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && !strings.HasSuffix(e.Name(), ".schema.json") {
				tool := strings.TrimSuffix(e.Name(), ".json")
				names = append(names, "mcp__"+serverName+"__"+tool)
			}
		}
	}
	if extraBuiltins != "" {
		names = append(names, strings.Split(extraBuiltins, ",")...)
	}
	return strings.Join(names, ",")
}

func miniMCPAllowedTools(extraBuiltins string) string {
	tools := "mcp__mini__list,mcp__mini__call,mcp__mini__perm_call,mcp__mini__config"
	if extraBuiltins != "" {
		tools += "," + extraBuiltins
	}
	return tools
}

func cliAllowedTools(extraBuiltins string) string {
	tools := "Bash"
	if extraBuiltins != "" {
		tools += "," + extraBuiltins
	}
	return tools
}

func preservedDir(t *testing.T, label string) string {
	t.Helper()
	d, err := os.MkdirTemp("", "mini-eval-"+label+"-*")
	if err != nil {
		t.Fatal("mktemp:", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("%s dir preserved for debugging: %s", label, d)
			return
		}
		os.RemoveAll(d) //nolint:errcheck
	})
	return d
}

func freshWorkDir(t *testing.T, workSrcDir string) string {
	t.Helper()
	if workSrcDir == "" {
		return ""
	}
	d, err := os.MkdirTemp("", "mini-eval-work-*")
	if err != nil {
		t.Fatal("mktemp workdir:", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("workdir preserved for debugging: %s", d)
			return
		}
		os.RemoveAll(d) //nolint:errcheck
	})
	if err := copyDir(workSrcDir, d); err != nil {
		t.Fatal("copy workdir:", err)
	}
	return d
}

func buildClaudeCmd(mcpConfigFile, allowedTools, workDir, task string) *exec.Cmd {
	args := []string{"--print", "--output-format", "json", task,
		"--strict-mcp-config", "--mcp-config", mcpConfigFile, "--no-session-persistence",
		"--allowedTools", allowedTools}
	if workDir != "" {
		args = append(args, "--add-dir", workDir)
	}
	cmd := exec.Command("claude", args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

func buildClaudeCLICmd(wrapperDir, allowedTools, workDir, task string) *exec.Cmd {
	preamble := "You have access to MCP servers via the CLI tool `mini`. " +
		"Run `mini -h` to learn more and `mini ls` to list available servers.\n\nTask: "
	args := []string{"--print", "--output-format", "json", preamble + task,
		"--no-session-persistence", "--allowedTools", allowedTools}
	if workDir != "" {
		args = append(args, "--add-dir", workDir)
	}
	cmd := exec.Command("claude", args...)
	cmd.Env = append(os.Environ(), "PATH="+wrapperDir+":"+os.Getenv("PATH"))
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

// writeMiniWrapper writes a shell script named "mini" that wraps the real binary
// with the config dir already baked in. Returns the directory containing the script.
func writeMiniWrapper(t *testing.T, configDir string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nexec " + miniBin + " --config " + configDir + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "mini"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runClaudeCmd runs a Claude command and returns its stdout.
// Safe to call from goroutines — returns errors instead of calling t.Fatal.
func runClaudeCmd(cmd *exec.Cmd, outputDir string) (string, error) {
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf

	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()

	select {
	case err := <-done:
		if err != nil {
			saveOutput(outputDir, "claude-output-partial.json", out.String())
			stderr := strings.TrimSpace(errBuf.String())
			if stderr != "" {
				return "", fmt.Errorf("claude: %v\nstderr: %s", err, stderr)
			}
			return "", fmt.Errorf("claude: %v", err)
		}
	case <-time.After(420 * time.Second):
		cmd.Process.Kill() //nolint:errcheck
		saveOutput(outputDir, "claude-output-partial.json", out.String())
		return "", fmt.Errorf("claude eval timed out after 420s")
	}
	return out.String(), nil
}

// saveOutput writes content to dir/name and returns the path. Silent on error.
func saveOutput(dir, name, content string) string {
	if dir == "" || content == "" {
		return ""
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return ""
	}
	return path
}

// miniMCPConfig builds a mini config dir and returns an MCP config JSON path.
func miniMCPConfig(t *testing.T, servers map[string]string, callLogDir string, fmt int) string {
	t.Helper()
	configDir := buildMiniConfigDir(t, servers, callLogDir, fmt)
	return writeMCPConfig(t, map[string]any{
		"mcpServers": map[string]any{
			"mini": map[string]any{
				"command": miniBin,
				"args":    []string{"--config", configDir, "serve", "--standalone", "--log-level", "error"},
			},
		},
	})
}

// miniCLIConfigDir builds a mini config dir for CLI mode.
func miniCLIConfigDir(t *testing.T, servers map[string]string, callLogDir string, fmt int) string {
	t.Helper()
	return buildMiniConfigDir(t, servers, callLogDir, fmt)
}

// buildMiniConfigDir creates a mini config dir with the given format and servers.
func buildMiniConfigDir(t *testing.T, servers map[string]string, callLogDir string, format int) string {
	t.Helper()
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(miniConfigYAML(format)), 0600); err != nil {
		t.Fatal(err)
	}
	writeServersYAML(t, configDir, servers, callLogDir)
	if format != fmtPassthrough {
		writeBundledProjections(t, configDir, servers)
	}
	return configDir
}

func miniConfigYAML(format int) string {
	switch format {
	case fmtPassthrough:
		return "inline_threshold: 9999999\nresponse_format: json\n"
	case fmtProjected:
		return "inline_threshold: 50000\nresponse_format: json\n"
	case fmtLines:
		return "inline_threshold: 50000\nresponse_format: mini\n"
	default:
		panic(fmt.Sprintf("unknown format %d", format))
	}
}

// writeBundledProjections copies bundled default projections for known servers
// from internal/defaults/projections/ into the config dir.
func writeBundledProjections(t *testing.T, configDir string, servers map[string]string) {
	t.Helper()
	projDir := filepath.Join(configDir, "projections")
	if err := os.MkdirAll(projDir, 0700); err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(repoRoot, "internal", "defaults", "projections")
	for name := range servers {
		src := filepath.Join(srcDir, name+".yaml")
		data, err := os.ReadFile(src)
		if err != nil {
			continue // no bundled projection for this server — skip silently
		}
		if err := os.WriteFile(filepath.Join(projDir, name+".yaml"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func writeServersYAML(t *testing.T, configDir string, servers map[string]string, callLogDir string) {
	t.Helper()
	serverDir := filepath.Join(configDir, "servers")
	if err := os.MkdirAll(serverDir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, fixtureDir := range servers {
		yaml := buildServerYAML(name, fixtureDir, callLogDir)
		if err := os.WriteFile(filepath.Join(serverDir, name+".yaml"), []byte(yaml), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func buildServerYAML(name, fixtureDir, callLogDir string) string {
	y := "name: " + name + "\ncommand: " + fakemcpBin + "\nargs:\n  - --fixtures\n  - " + fixtureDir + "\n"
	if callLogDir != "" {
		y += "  - --call-log\n  - " + filepath.Join(callLogDir, name+".log") + "\n"
	}
	return y
}

// rawMCPConfig writes an MCP config pointing Claude directly at fakemcp.
func rawMCPConfig(t *testing.T, servers map[string]string, callLogDir string) string {
	t.Helper()
	mcpServers := make(map[string]any, len(servers))
	for name, fixtureDir := range servers {
		mcpServers[name] = map[string]any{
			"command": fakemcpBin,
			"args":    fakemcpArgs(fixtureDir, callLogDir, name),
		}
	}
	return writeMCPConfig(t, map[string]any{"mcpServers": mcpServers})
}

func fakemcpArgs(fixtureDir, callLogDir, serverName string) []string {
	args := []string{"--fixtures", fixtureDir}
	if callLogDir != "" {
		args = append(args, "--call-log", filepath.Join(callLogDir, serverName+".log"))
	}
	return args
}

func writeMCPConfig(t *testing.T, cfg map[string]any) string {
	t.Helper()
	b, _ := json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func repoDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "repo")
}

type claudeOutput struct {
	Result    string  `json:"result"`
	TotalCost float64 `json:"total_cost_usd"`
	NumTurns  int     `json:"num_turns"`
	Usage     struct {
		InputTokens      int `json:"input_tokens"`
		CacheReadTokens  int `json:"cache_read_input_tokens"`
		CacheWriteTokens int `json:"cache_creation_input_tokens"`
		OutputTokens     int `json:"output_tokens"`
	} `json:"usage"`
}

func parseClaudeResult(output string) ClaudeResult {
	var data claudeOutput
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return ClaudeResult{Text: output}
	}
	return ClaudeResult{
		Text:             data.Result,
		InputTokens:      data.Usage.InputTokens,
		CacheReadTokens:  data.Usage.CacheReadTokens,
		CacheWriteTokens: data.Usage.CacheWriteTokens,
		OutputTokens:     data.Usage.OutputTokens,
		TotalCostUSD:     data.TotalCost,
		Turns:            data.NumTurns,
	}
}

func toolCallCounts(callLogDir, server string) map[string]int {
	data, err := os.ReadFile(filepath.Join(callLogDir, server+".log"))
	if err != nil {
		return nil
	}
	counts := make(map[string]int)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry struct {
			Tool string `json:"tool"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Tool != "" {
			counts[entry.Tool]++
		}
	}
	return counts
}

func assertServerCalled(t *testing.T, callLogDir, server string) {
	t.Helper()
	if len(toolCallCounts(callLogDir, server)) == 0 {
		t.Errorf("expected %s to be called, but call log is empty or missing (dir: %s)", server, callLogDir)
	}
}

func assertToolCalled(t *testing.T, callLogDir, server, tool string) {
	t.Helper()
	counts := toolCallCounts(callLogDir, server)
	if counts[tool] == 0 {
		t.Errorf("expected %s.%s to be called — actual calls: %v", server, tool, counts)
	}
}

func assertAnyToolCalled(t *testing.T, callLogDir, server string, tools ...string) {
	t.Helper()
	counts := toolCallCounts(callLogDir, server)
	for _, tool := range tools {
		if counts[tool] > 0 {
			return
		}
	}
	t.Errorf("expected one of %v on %s to be called — actual calls: %v", tools, server, counts)
}

func assertResponseContains(t *testing.T, label, text string, want ...string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, w := range want {
		if strings.Contains(lower, strings.ToLower(w)) {
			return
		}
	}
	t.Errorf("[%s] response does not mention any of %v\nResponse: %s", label, want, text[:min(300, len(text))])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
