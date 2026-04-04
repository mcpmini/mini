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
)

func TestMain(m *testing.M) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..")
	fixturesDir = filepath.Join(root, "benchmarks", "fixtures")
	miniBin = mustBuildEvalBinary(root, "mini", "./cmd/mini")
	fakemcpBin = mustBuildEvalBinary(root, "fakemcp", "./test/fakemcp", "-tags", "integration")
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

type ClaudeResult struct {
	Text             string
	InputTokens      int
	CacheReadTokens  int
	CacheWriteTokens int
	OutputTokens     int
	TotalCostUSD     float64
	Turns            int
	WorkDir          string
	// CallLogDir contains per-server call logs: <CallLogDir>/<server>.log
	// Each line is a JSON object {"tool":"name","ts":"..."}.
	CallLogDir string
	// RawOutputPath is the path to the full JSON output from the claude CLI,
	// useful for post-run inspection of the tool call chain.
	RawOutputPath string
}

func (r ClaudeResult) EffectiveInputTokens() int {
	return r.InputTokens + r.CacheReadTokens + r.CacheWriteTokens
}

type TripleResult struct {
	Raw   ClaudeResult
	JSON  ClaudeResult
	Lines ClaudeResult
}

// Savings use EffectiveInputTokens because tool definitions land in the cache
// and would appear as zero-cost if we only counted non-cached input tokens.
func logTriple(t *testing.T, label string, r TripleResult) {
	t.Helper()
	savJSON, savLines := 0.0, 0.0
	rawEff := r.Raw.EffectiveInputTokens()
	if rawEff > 0 {
		savJSON = float64(rawEff-r.JSON.EffectiveInputTokens()) / float64(rawEff) * 100
		savLines = float64(rawEff-r.Lines.EffectiveInputTokens()) / float64(rawEff) * 100
	}
	t.Logf("\n╔══ Token Report: %s ══╗", label)
	t.Logf("  Raw           %6d effective in (%d+%d+%d) + %4d out  $%.4f  (%d turns)",
		rawEff, r.Raw.InputTokens, r.Raw.CacheReadTokens, r.Raw.CacheWriteTokens,
		r.Raw.OutputTokens, r.Raw.TotalCostUSD, r.Raw.Turns)
	t.Logf("  Minimcp JSON  %6d effective in (%d+%d+%d) + %4d out  $%.4f  (%d turns)  %.1f%% saved",
		r.JSON.EffectiveInputTokens(), r.JSON.InputTokens, r.JSON.CacheReadTokens, r.JSON.CacheWriteTokens,
		r.JSON.OutputTokens, r.JSON.TotalCostUSD, r.JSON.Turns, savJSON)
	t.Logf("  Minimcp Lines %6d effective in (%d+%d+%d) + %4d out  $%.4f  (%d turns)  %.1f%% saved",
		r.Lines.EffectiveInputTokens(), r.Lines.InputTokens, r.Lines.CacheReadTokens, r.Lines.CacheWriteTokens,
		r.Lines.OutputTokens, r.Lines.TotalCostUSD, r.Lines.Turns, savLines)
	t.Logf("╚══════════════════════════════════════╝")
	t.Logf("[raw]   %s", r.Raw.Text)
	t.Logf("[json]  %s", r.JSON.Text)
	t.Logf("[lines] %s", r.Lines.Text)
}

type evalParams struct {
	servers      map[string]string
	allowedTools string
	workSrcDir   string
}

func runTriple(t *testing.T, p evalParams, task string) TripleResult {
	t.Helper()
	rawCallDir, jsonCallDir, linesCallDir := preservedDir(t, "raw"), preservedDir(t, "json"), preservedDir(t, "lines")
	rawCfg := rawMCPConfig(t, p.servers, rawCallDir)
	jsonCfg := miniConfig(t, p.servers, "json", jsonCallDir)
	linesCfg := miniConfig(t, p.servers, "lines", linesCallDir)
	rawTools := rawAllowedTools(p.servers, p.allowedTools)
	proxyTools := miniAllowedTools(p.allowedTools)
	return TripleResult{
		Raw:   runClaude(t, rawCfg, rawTools, freshWorkDir(t, p.workSrcDir), rawCallDir, task),
		JSON:  runClaude(t, jsonCfg, proxyTools, freshWorkDir(t, p.workSrcDir), jsonCallDir, task),
		Lines: runClaude(t, linesCfg, proxyTools, freshWorkDir(t, p.workSrcDir), linesCallDir, task),
	}
}

func miniAllowedTools(extraBuiltins string) string {
	tools := "mcp__mini__list,mcp__mini__call,mcp__mini__perm_call,mcp__mini__config"
	if extraBuiltins != "" {
		tools += "," + extraBuiltins
	}
	return tools
}

// Claude requires explicit tool names; glob patterns like mcp__server__* are not supported.
func rawAllowedTools(servers map[string]string, extraBuiltins string) string {
	names := rawToolNames(servers)
	if extraBuiltins != "" {
		names = append(names, extraBuiltins)
	}
	return strings.Join(names, ",")
}

func rawToolNames(servers map[string]string) []string {
	var names []string
	for serverName, dir := range servers {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				tool := strings.TrimSuffix(e.Name(), ".json")
				names = append(names, "mcp__"+serverName+"__"+tool)
			}
		}
	}
	return names
}

// preservedDir creates a temp directory that is kept on test failure for debugging.
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
	d, err := os.MkdirTemp("", "mini-eval-*")
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
	// task before variadic flags: the CLI parser would otherwise consume it as part of a flag argument.
	args := []string{"--print", "--output-format", "json", task,
		"--strict-mcp-config", "--mcp-config", mcpConfigFile, "--no-session-persistence",
		"--allowedTools", allowedTools}
	if workDir != "" {
		args = append(args, "--add-dir", workDir)
	}
	return exec.Command("claude", args...)
}

func runClaudeCmd(t *testing.T, cmd *exec.Cmd, outputDir string) string {
	t.Helper()
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		logClaudeStderr(t, &errBuf)
		if err != nil {
			saveOutput(t, outputDir, "claude-output-partial.json", out.String())
			t.Fatalf("claude: %v", err)
		}
	case <-time.After(240 * time.Second):
		cmd.Process.Kill() //nolint:errcheck
		logClaudeStderr(t, &errBuf)
		saveOutput(t, outputDir, "claude-output-partial.json", out.String())
		t.Fatal("claude eval timed out after 240s")
	}
	return out.String()
}

func logClaudeStderr(t *testing.T, errBuf *strings.Builder) {
	t.Helper()
	if errBuf.Len() > 0 {
		t.Logf("claude stderr:\n%s", errBuf.String())
	}
}

func saveOutput(t *testing.T, dir, name, content string) {
	t.Helper()
	if dir == "" || content == "" {
		return
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Logf("warning: could not save output: %v", err)
		return
	}
	t.Logf("output saved: %s", path)
}

func runClaude(t *testing.T, mcpConfigFile, allowedTools, workDir, callLogDir, task string) ClaudeResult {
	t.Helper()
	cmd := buildClaudeCmd(mcpConfigFile, allowedTools, workDir, task)
	evalCWD := workDir
	if evalCWD == "" {
		evalCWD = freshWorkDir(t, repoDir())
	}
	cmd.Dir = evalCWD
	rawOutput := runClaudeCmd(t, cmd, callLogDir)
	outputPath := filepath.Join(callLogDir, "claude-output.json")
	if err := os.WriteFile(outputPath, []byte(rawOutput), 0600); err != nil {
		t.Logf("warning: could not save claude output: %v", err)
		outputPath = ""
	}
	result := parseClaudeResult(rawOutput)
	result.WorkDir = workDir
	result.CallLogDir = callLogDir
	result.RawOutputPath = outputPath
	return result
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

func miniConfig(t *testing.T, servers map[string]string, format, callLogDir string) string {
	t.Helper()
	configDir := t.TempDir()
	cfg := fmt.Sprintf("inline_threshold: 50000\nresponse_format: %s\n", format)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	writeServersYAML(t, configDir, servers, callLogDir)
	return writeMCPConfig(t, map[string]any{
		"mcpServers": map[string]any{
			"mini": map[string]any{
				"command": miniBin,
				"args":    []string{"--config", configDir, "serve", "--standalone", "--log-level", "error"},
			},
		},
	})
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

// toolCallCounts reads the JSON-line call log for a given server and returns
// a map of tool name → call count. Returns nil if the log doesn't exist.
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

// assertServerCalled fails the test if no tool on the given server was called.
func assertServerCalled(t *testing.T, callLogDir, server string) {
	t.Helper()
	counts := toolCallCounts(callLogDir, server)
	if len(counts) == 0 {
		t.Errorf("expected %s to be called, but call log is empty or missing (dir: %s)", server, callLogDir)
	}
}

// assertToolCalled fails the test if the specific tool on the server was not called.
func assertToolCalled(t *testing.T, callLogDir, server, tool string) {
	t.Helper()
	counts := toolCallCounts(callLogDir, server)
	if counts[tool] == 0 {
		t.Errorf("expected %s.%s to be called — actual calls: %v", server, tool, counts)
	}
}

// assertResponseContains fails the test if the response text contains none of the given substrings.
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

// tripleWithLabels returns the three result modes (raw, json, lines) as a slice
// with labels, for range-based iteration in eval assertions.
func tripleWithLabels(r TripleResult) []struct {
	label  string
	result ClaudeResult
} {
	return []struct {
		label  string
		result ClaudeResult
	}{
		{"raw", r.Raw},
		{"json", r.JSON},
		{"lines", r.Lines},
	}
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
