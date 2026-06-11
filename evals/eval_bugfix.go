//go:build evals

package evals

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const bugFixPipelineTask = "Find the highest-priority open bug in Jira, look it up in Sentry for error details, fix it in the codebase, and open a GitHub pull request referencing the Jira ticket."

// RunBugfixEval exercises the full agent bug-fix loop: Jira → Sentry → code fix → GitHub PR.
func RunBugfixEval(ctx context.Context, r *Runner, env *Env) (EvalResult, []error) {
	servers, err := DefaultServers(r, env, "jira", "sentry", "github")
	if err != nil {
		return EvalResult{}, []error{err}
	}
	result, err := r.RunEval(ctx, evalCtx{Env: env, Params: EvalParams{
		Servers:      servers,
		AllowedTools: "Read,Edit,Write",
		WorkSrcDir:   bugfixTestdataDir(),
	}, Task: bugFixPipelineTask})
	if err != nil {
		return EvalResult{}, []error{err}
	}
	LogEval(logWriter(), "Bug fix pipeline (Jira → Sentry → code fix → GitHub PR)", result)
	return result, assertBugfixResult(result)
}

func assertBugfixResult(result EvalResult) []error {
	var errs []error
	for _, tc := range EvalWithLabels(result) {
		for i, run := range tc.Stats.Runs {
			errs = append(errs, assertBugfixRun(RepLabel(tc.Label, i, len(tc.Stats.Runs)), run)...)
		}
	}
	return errs
}

func assertBugfixRun(label string, run ClaudeResult) []error {
	var errs []error
	add := labeledAdder(label, &errs)
	if run.Err != nil {
		add(fmt.Errorf("run failed: %w (logs: %s)", run.Err, run.CallLogDir))
		return errs
	}
	add(AssertToolCalled(run.CallLogDir, "jira", "search_issues"))
	add(AssertToolCalled(run.CallLogDir, "sentry", "list_issues"))
	add(AssertToolCalled(run.CallLogDir, "github", "create_pull_request"))
	add(verifyBugFix(run.WorkDir))
	add(AssertResponseContains(run.Text, "WEBAPP-441", "JWT"))
	if run.Turns < 4 {
		errs = append(errs, fmt.Errorf("[%s] expected at least 4 turns, got %d", label, run.Turns))
	}
	return errs
}

func labeledAdder(label string, errs *[]error) func(error) {
	return func(err error) {
		if err != nil {
			*errs = append(*errs, fmt.Errorf("[%s] %w", label, err))
		}
	}
}

// verifyBugFix checks that auth/middleware.go was modified to reject alg:none tokens.
func verifyBugFix(workDir string) error {
	if workDir == "" {
		return nil
	}
	content, err := os.ReadFile(filepath.Join(workDir, "auth", "middleware.go"))
	if err != nil {
		return fmt.Errorf("auth/middleware.go not found in workdir: %w", err)
	}
	if bytes.Contains(content, []byte("alg:none")) || bytes.Contains(content, []byte("alg == \"none\"")) ||
		bytes.Contains(content, []byte("WithValidMethods")) || bytes.Contains(content, []byte("allowedAlgorithms")) {
		return nil
	}
	return fmt.Errorf("auth/middleware.go does not appear to include an algorithm validation fix")
}

func bugfixTestdataDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "testdata", "bugfix")
}
