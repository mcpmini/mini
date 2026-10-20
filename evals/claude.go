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

type claudeMCPCommandParams struct {
	ConfigFile   string
	AllowedTools string
	WorkDir      string
	Task         string
}

func buildClaudeCmd(p claudeMCPCommandParams) *exec.Cmd {
	args := []string{
		"--print", "--output-format", "json", "--model", "claude-sonnet-4-6", p.Task,
		"--strict-mcp-config", "--mcp-config", p.ConfigFile,
		"--no-session-persistence", "--allowedTools", p.AllowedTools,
	}
	if p.WorkDir != "" {
		args = append(args, "--add-dir", p.WorkDir)
	}
	cmd := exec.Command("claude", args...)
	if p.WorkDir != "" {
		cmd.Dir = p.WorkDir
	}
	return cmd
}

type claudeCLICommandParams struct {
	WrapperDir   string
	AllowedTools string
	WorkDir      string
	Task         string
}

func buildClaudeCLICmd(p claudeCLICommandParams) *exec.Cmd {
	preamble := "You have access to MCP servers via the CLI tool `mini`. " +
		"Run `mini -h` to learn more and `mini ls` to list available servers.\n\nTask: "
	args := []string{
		"--print", "--output-format", "json", "--model", "claude-sonnet-4-6", preamble + p.Task,
		"--no-session-persistence", "--allowedTools", p.AllowedTools,
	}
	if p.WorkDir != "" {
		args = append(args, "--add-dir", p.WorkDir)
	}
	cmd := exec.Command("claude", args...)
	cmd.Env = append(os.Environ(), "PATH="+p.WrapperDir+":"+os.Getenv("PATH"))
	if p.WorkDir != "" {
		cmd.Dir = p.WorkDir
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
	timer := time.NewTimer(420 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return handleClaudeRunError(claudeRunErrorParams{
				Err: err, OutputDir: outputDir, Output: out.String(), Stderr: errBuf.String(),
			})
		}
	case <-timer.C:
		return handleClaudeTimeout(cmd, outputDir, out.String())
	}
	return out.String(), nil
}

func handleClaudeTimeout(cmd *exec.Cmd, outputDir, output string) (string, error) {
	cmd.Process.Kill() //nolint:errcheck
	saveOutput(outputDir, "claude-output-partial.json", output)
	return "", fmt.Errorf("claude eval timed out after 420s")
}

type claudeRunErrorParams struct {
	Err       error
	OutputDir string
	Output    string
	Stderr    string
}

func handleClaudeRunError(p claudeRunErrorParams) (string, error) {
	saveOutput(p.OutputDir, "claude-output-partial.json", p.Output)
	if p.Stderr = strings.TrimSpace(p.Stderr); p.Stderr != "" {
		return p.Output, fmt.Errorf("claude: %v\nstderr: %s", p.Err, p.Stderr)
	}
	return p.Output, fmt.Errorf("claude: %v", p.Err)
}

func saveOutput(dir, name, content string) string {
	if dir == "" || content == "" {
		return ""
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
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
