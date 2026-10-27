package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const scenarioTimeout = 5 * time.Minute

type outcome struct {
	Set      string
	Scenario string
	ExitCode int
	Problems []check
}

func (o outcome) passed() bool {
	return o.ExitCode == 0
}

func anyFailed(outcomes []outcome) bool {
	for _, o := range outcomes {
		if !o.passed() {
			return true
		}
	}
	return false
}

func runScenario(env runEnv, set scenarioSet, scenario string) (outcome, error) {
	dir := filepath.Join(env.Results, set.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return outcome{}, err
	}
	output, exitCode, err := runSuite(env, set, scenario)
	if err != nil {
		return outcome{}, err
	}
	logPath := filepath.Join(dir, strings.ReplaceAll(scenario, "/", "_")+".log")
	if writeErr := os.WriteFile(logPath, output, 0o600); writeErr != nil {
		return outcome{}, writeErr
	}
	problems, err := scenarioProblems(output)
	return outcome{Set: set.Name, Scenario: scenario, ExitCode: exitCode, Problems: problems}, err
}

func runSuite(env runEnv, set scenarioSet, scenario string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), scenarioTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, env.Suite, "client", "--command", env.Adapter,
		"--scenario", scenario, "--spec-version", set.Version, "--output-dir", filepath.Join(env.Results, set.Name))
	cmd.Env = append(os.Environ(), "MINI_BIN="+env.Mini, "MINI_CONFORMANCE_MODE="+set.Mode)
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return output, exitErr.ExitCode(), nil
	}
	return output, 0, err
}

func scenarioProblems(output []byte) ([]check, error) {
	dir, ok := resultsDir(output)
	if !ok {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "checks.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return problemChecks(data)
}
