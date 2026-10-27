package main

import (
	"bytes"
	"context"
	"encoding/json"
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

func (result cliResult) jsonOutput() map[string]any {
	var value map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &value); err != nil {
		return map[string]any{}
	}
	return value
}

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
	if result.err != nil {
		result.stdout = "{}"
	}
	return result.jsonOutput(), nil
}
