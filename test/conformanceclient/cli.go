package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type cliResult struct {
	stdout string
	stderr string
	err    error
}

type cliParams struct {
	Binary  string
	Config  string
	Timeout time.Duration
	Args    []string
}

func runCLI(params cliParams) cliResult {
	ctx, cancel := context.WithTimeout(context.Background(), params.Timeout)
	defer cancel()
	command := exec.CommandContext(ctx, params.Binary, append([]string{"--config", params.Config}, params.Args...)...)
	command.Stdin = strings.NewReader("")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func parseToolTable(output string) []tool {
	var tools []tool
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "TOOL" || strings.HasSuffix(strings.TrimSpace(line), ":") {
			continue
		}
		tools = append(tools, tool{Name: fields[0]})
	}
	return tools
}

func (result cliResult) combined() string { return result.stdout + result.stderr }

type cliCallParams struct {
	Binary    string
	Config    string
	Name      string
	Arguments map[string]any
}

func callCLI(params cliCallParams) (map[string]any, error) {
	encoded, err := json.Marshal(params.Arguments)
	if err != nil {
		return nil, err
	}
	result := runCLI(cliParams{
		Binary: params.Binary, Config: params.Config, Timeout: 60 * time.Second,
		Args: []string{"call", "conf", params.Name, string(encoded)},
	})
	return callOutput(params.Name, result)
}

func callOutput(name string, result cliResult) (map[string]any, error) {
	if result.err != nil {
		return nil, fmt.Errorf("mini call %s: %w: %s", name, result.err, result.combined())
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &envelope); err != nil {
		return nil, fmt.Errorf("mini call %s: decode output: %w", name, err)
	}
	return envelope, nil
}

func listOutput(result cliResult) ([]tool, error) {
	if result.err != nil {
		return nil, fmt.Errorf("mini ls: %w: %s", result.err, result.combined())
	}
	return parseToolTable(result.stdout), nil
}
