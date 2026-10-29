package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

const (
	suiteTimeout      = 90 * time.Second
	hungSuiteDeadline = suiteTimeout + 15*time.Second
)

func suiteArgs(env runEnv, set scenarioSet, scenario string) []string {
	return []string{
		"client", "--command", env.Adapter, "--scenario", scenario,
		"--spec-version", set.Version, "--timeout", strconv.FormatInt(suiteTimeout.Milliseconds(), 10),
		"--output-dir", filepath.Join(env.Results, set.Name),
	}
}

func runSuite(run scenarioRun) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), hungSuiteDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, run.Env.Suite, suiteArgs(run.Env, run.Set, run.Name)...)
	// test-bins.sh builds mini with -race, whose default one-second exit delay would add up over every CLI call.
	cmd.Env = append(os.Environ(), "MINI_CONFORMANCE_MODE="+run.Set.Mode, "GORACE=atexit_sleep_ms=0")
	output, err := cmd.CombinedOutput()
	exitCode, runErr := commandResult(ctx, err)
	return output, exitCode, runErr
}

func commandResult(ctx context.Context, err error) (int, error) {
	if ctx.Err() != nil {
		return -1, fmt.Errorf("runner scenario deadline: %w", ctx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
