package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
)

const (
	serverName = "conf"
	cliTimeout = 60 * time.Second
)

// miniCLI runs one mini binary against one scenario's config dir.
type miniCLI struct {
	bin       string
	configDir string
}

type cliResult struct {
	stdout string
	stderr string
	err    error
}

func (r cliResult) combined() string { return r.stdout + r.stderr }

func (r cliResult) failure(operation string) error {
	if r.err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w: %s", operation, r.err, r.combined())
}

func (m miniCLI) run(args ...string) cliResult {
	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, m.bin, append([]string{"--config", m.configDir}, args...)...)
	command.Stdin = strings.NewReader("")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func (m miniCLI) add(url string, flags ...string) cliResult {
	return m.run(append([]string{"add", serverName, "--url", url}, flags...)...)
}

func (m miniCLI) auth() cliResult { return m.run("auth", serverName) }

func (m miniCLI) listTools() ([]string, error) {
	result := m.run("ls", serverName)
	if err := result.failure("mini ls"); err != nil {
		return nil, err
	}
	return parseToolTable(result.stdout), nil
}

func (m miniCLI) call(call toolCall) error {
	encoded, err := json.Marshal(call.Arguments)
	if err != nil {
		return err
	}
	result := m.run("call", serverName, call.Name, string(encoded))
	if err := result.failure("mini call " + call.Name); err != nil {
		return err
	}
	if !json.Valid([]byte(result.stdout)) {
		return fmt.Errorf("mini call %s: output is not JSON: %s", call.Name, result.stdout)
	}
	return nil
}

func (m miniCLI) writeConfig(cfg config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.configDir, "config.yaml"), data, 0o600)
}

func (m miniCLI) createServer(sc config.ServerConfig) error {
	_, err := config.CreateServerFile(m.configDir, sc)
	return err
}

// parseToolTable reads tool names from `mini ls <server>`, which only prints a table.
func parseToolTable(output string) []string {
	var names []string
	inRows := false
	for line := range strings.Lines(output) {
		fields := strings.Fields(line)
		switch {
		case strings.TrimSpace(line) == "No tools available.":
			return nil
		case len(fields) == 0 && inRows:
			return names
		case len(fields) == 0:
		case inRows:
			names = append(names, fields[0])
		case len(fields) >= 2 && fields[0] == "TOOL" && fields[1] == "HINTS":
			inRows = true
		}
	}
	return names
}

// isUnauthorized matches mini's CLI error text; the CLI reports no machine-readable auth failure.
func isUnauthorized(err error) bool {
	text := err.Error()
	return strings.Contains(text, "unauthorized (") ||
		strings.Contains(text, "status 401") || strings.Contains(text, "status 403")
}
