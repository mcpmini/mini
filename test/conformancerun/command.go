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

type runLimits struct {
	Suite    time.Duration
	Scenario time.Duration
	Wait     time.Duration
	Total    time.Duration
}

func (limits runLimits) defaults() runLimits {
	if limits.Suite == 0 {
		limits.Suite = 90 * time.Second
	}
	if limits.Scenario == 0 {
		limits.Scenario = 105 * time.Second
	}
	if limits.Wait == 0 {
		limits.Wait = 5 * time.Second
	}
	if limits.Total == 0 {
		limits.Total = 10 * time.Minute
	}
	return limits
}

func suiteArgs(env runEnv, set scenarioSet, scenario string) []string {
	return []string{
		"client", "--command", env.Adapter, "--scenario", scenario,
		"--spec-version", set.Version, "--timeout", strconv.FormatInt(env.Limits.defaults().Suite.Milliseconds(), 10),
		"--output-dir", filepath.Join(env.Results, set.Name),
	}
}

func runSuite(parent context.Context, run scenarioRun) ([]byte, int, error) {
	env, set, scenario := run.Env, run.Set, run.Name
	ctx, cancel := context.WithTimeout(parent, env.Limits.defaults().Scenario)
	defer cancel()
	cmd := exec.CommandContext(ctx, env.Suite, suiteArgs(env, set, scenario)...)
	cmd.Env = append(os.Environ(), "MINI_BIN="+env.Mini, "MINI_CONFORMANCE_MODE="+set.Mode)
	cmd.WaitDelay = env.Limits.defaults().Wait
	cleanup := ownProcessGroup(cmd)
	defer cleanup()
	output, err := cmd.CombinedOutput()
	exitCode, runErr := commandResult(parent, ctx, err)
	return output, exitCode, runErr
}

func commandResult(parent, ctx context.Context, err error) (int, error) {
	if parent.Err() != nil {
		return -1, fmt.Errorf("total scenario budget exhausted: %w", parent.Err())
	}
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
