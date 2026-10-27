package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	env, cleanup, err := prepare()
	if err != nil {
		return false, err
	}
	defer cleanup()
	outcomes, err := runSets(env, selected)
	if err != nil {
		return false, err
	}
	return anyFailed(outcomes), report(env.Results, outcomes)
}

type runEnv struct {
	Suite   string
	Adapter string
	Mini    string
	Results string
}

func prepare() (runEnv, func(), error) {
	if _, err := os.Stat(suitePath); err != nil {
		return runEnv{}, nil, fmt.Errorf("%w; run npm ci --prefix test/conformance from the repository root", err)
	}
	results, err := resultsRoot()
	if err != nil {
		return runEnv{}, nil, err
	}
	build, cleanup, err := buildDir()
	if err != nil {
		return runEnv{}, nil, err
	}
	env := runEnv{
		Suite: suitePath, Results: results,
		Mini: filepath.Join(build, "mini"), Adapter: filepath.Join(build, "conformanceclient"),
	}
	if err := buildBinaries(env); err != nil {
		cleanup()
		return runEnv{}, nil, err
	}
	return env, cleanup, nil
}

func buildDir() (string, func(), error) {
	dir, err := os.MkdirTemp("", "mini-conformance-build-")
	if err != nil {
		return "", nil, err
	}
	return dir, func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			fmt.Fprintln(os.Stderr, "remove build dir:", removeErr)
		}
	}, nil
}

func resultsRoot() (string, error) {
	if dir := os.Getenv("CONFORMANCE_RESULTS"); dir != "" {
		return dir, os.MkdirAll(dir, 0o700)
	}
	return os.MkdirTemp("", "mini-conformance-results-")
}

func buildBinaries(env runEnv) error {
	builds := [][2]string{{env.Mini, "./cmd/mini"}, {env.Adapter, "./test/conformanceclient"}}
	for _, target := range builds {
		build := exec.CommandContext(context.Background(), "go", "build", "-o", target[0], target[1])
		output, err := build.CombinedOutput()
		if err != nil {
			return fmt.Errorf("go build %s: %w\n%s", target[1], err, output)
		}
	}
	return nil
}

func runSets(env runEnv, selected []scenarioSet) ([]outcome, error) {
	var outcomes []outcome
	// auth scenarios log in through mini's fixed OAuth callback port 6464, so scenarios can't run in parallel.
	for _, set := range selected {
		for _, scenario := range set.Scenarios {
			result, err := runScenario(env, set, scenario)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", set.Name, scenario, err)
			}
			fmt.Printf("%s: %s (exit %d)\n", set.Name, scenario, result.ExitCode)
			outcomes = append(outcomes, result)
		}
	}
	return outcomes, nil
}

func report(results string, outcomes []outcome) error {
	summary := renderSummary(outcomes)
	fmt.Printf("\n%s\nConformance results: %s\n", summary, results)
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(summary)
	return errors.Join(writeErr, file.Close())
}
