package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type outcome struct {
	Set      string
	Scenario string
	ExitCode int
	Ran      bool
	Problems []check
	Error    string
}

func (o outcome) passed() bool {
	return o.Error == "" && o.ExitCode == 0 && o.Ran && len(o.Problems) == 0
}

func anyFailed(outcomes []outcome) bool {
	for _, o := range outcomes {
		if !o.passed() {
			return true
		}
	}
	return false
}

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
	if runErr == nil && strings.Contains(string(output), "Client timed out") {
		runErr = errors.New("suite client timeout")
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
