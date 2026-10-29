package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type scenarioRun struct {
	Env  runEnv
	Set  scenarioSet
	Name string
}

func runScenario(run scenarioRun) (outcome, error) {
	env, set, scenario := run.Env, run.Set, run.Name
	result := outcome{Set: set.Name, Scenario: scenario, ExitCode: -1}
	dir := filepath.Join(env.Results, set.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return result, err
	}
	output, exitCode, runErr := runSuite(run)
	result.ExitCode = exitCode
	logPath := filepath.Join(dir, strings.ReplaceAll(scenario, "/", "_")+".log")
	writeErr := os.WriteFile(logPath, output, 0o600)
	problems, ran, parseErr := scenarioProblems(output, scenario)
	result.Problems, result.Ran = problems, ran
	if runErr == nil {
		runErr = clientTimeoutError(output)
	}
	return result, errors.Join(runErr, writeErr, parseErr)
}

func scenarioProblems(output []byte, scenario string) ([]check, bool, error) {
	dir, ok := resultsDir(output)
	if !ok {
		return nil, false, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "checks.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	problems, err := scenarioChecks(data, scenario)
	return problems, true, err
}

func resultsDir(output []byte) (string, bool) {
	for line := range strings.Lines(string(output)) {
		if dir, ok := strings.CutPrefix(line, "Results saved to "); ok {
			return strings.TrimSpace(dir), true
		}
	}
	return "", false
}

func clientTimeoutError(output []byte) error {
	if strings.Contains(string(output), "Client timed out") {
		return errors.New("suite client timeout")
	}
	return nil
}
