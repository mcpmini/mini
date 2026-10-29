package main

import (
	"fmt"
	"os"
)

const suitePath = "test/conformance/node_modules/.bin/conformance"

func main() {
	failed, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "conformance:", err)
		os.Exit(2)
	}
	if failed {
		os.Exit(1)
	}
}

func run(args []string) (bool, error) {
	selected, err := selectSets(args)
	if err != nil {
		return false, err
	}
	env, err := prepare()
	if err != nil {
		return false, err
	}
	outcomes, err := runSets(env, selected)
	if err != nil {
		return false, err
	}
	report(env.Results, outcomes)
	return anyFailed(outcomes), nil
}

type runEnv struct {
	Suite   string
	Adapter string
	Results string
}

func prepare() (runEnv, error) {
	if _, err := os.Stat(suitePath); err != nil {
		return runEnv{}, fmt.Errorf("%w; run npm ci --prefix test/conformance from the repository root", err)
	}
	adapter := os.Getenv("CONFORMANCE_CLIENT_BIN")
	if adapter == "" || os.Getenv("MINIMCP_BIN") == "" {
		return runEnv{}, fmt.Errorf(
			"MINIMCP_BIN and CONFORMANCE_CLIENT_BIN are required; export what scripts/test-bins.sh prints",
		)
	}
	results, err := resultsRoot()
	if err != nil {
		return runEnv{}, err
	}
	return runEnv{Suite: suitePath, Adapter: adapter, Results: results}, nil
}

func resultsRoot() (string, error) {
	if dir := os.Getenv("CONFORMANCE_RESULTS"); dir != "" {
		return dir, os.MkdirAll(dir, 0o700)
	}
	return os.MkdirTemp("", "mini-conformance-results-")
}

func runSets(env runEnv, selected []scenarioSet) ([]outcome, error) {
	var outcomes []outcome
	for _, set := range selected {
		// Auth scenarios share mini's fixed OAuth callback port and cannot overlap.
		for _, scenario := range set.Scenarios {
			result, err := runScenario(scenarioRun{Env: env, Set: set, Name: scenario})
			if err != nil {
				result.Error = err.Error()
			}
			outcomes = append(outcomes, result)
			fmt.Printf("%s: %s (exit %d)\n", set.Name, scenario, result.ExitCode)
			if err := saveSummary(env.Results, outcomes); err != nil {
				return outcomes, err
			}
		}
	}
	return outcomes, nil
}
