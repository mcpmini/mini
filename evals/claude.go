//go:build evals

package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func buildClaudeCmd(mcpConfigFile, allowedTools, workDir, task string) *exec.Cmd {
	args := []string{
		"--print", "--output-format", "json", "--model", "claude-sonnet-4-6", task,
		"--strict-mcp-config", "--mcp-config", mcpConfigFile,
		"--no-session-persistence", "--allowedTools", allowedTools,
	}
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
	args := []string{
		"--print", "--output-format", "json", "--model", "claude-sonnet-4-6", preamble + task,
		"--no-session-persistence", "--allowedTools", allowedTools,
	}
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

func isRateLimited(output string) bool {
	return strings.Contains(output, "You've hit your limit") ||
		strings.Contains(output, "rate limit") ||
		strings.Contains(output, "resets") && strings.Contains(output, "am (")
}

func runClaudeCmd(cmd *exec.Cmd, outputDir string) (string, error) {
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		if err != nil {
			output := out.String()
			saveOutput(outputDir, "claude-output-partial.json", output)
			if stderr := strings.TrimSpace(errBuf.String()); stderr != "" {
				return output, fmt.Errorf("claude: %v\nstderr: %s", err, stderr)
			}
			return output, fmt.Errorf("claude: %v", err)
		}
	case <-time.After(420 * time.Second):
		cmd.Process.Kill() //nolint:errcheck
		saveOutput(outputDir, "claude-output-partial.json", out.String())
		return "", fmt.Errorf("claude eval timed out after 420s")
	}
	return out.String(), nil
}

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
