//go:build evals

package evals

import (
	"context"
	"fmt"
)

const reviewPRsTask = "List the open pull requests. For each PR, fetch the changed files and read the relevant source files to understand what changed. Then post a brief code review comment on each PR summarizing: what the change does, any obvious risks, and whether it looks ready to merge."

// RunReviewPRsEval exercises a multi-step code review loop: list PRs → read files → comment.
func RunReviewPRsEval(ctx context.Context, r *Runner, env *Env) (EvalResult, []error) {
	servers, err := DefaultServers(r, env, "github")
	if err != nil {
		return EvalResult{}, []error{err}
	}
	result, err := r.RunEval(ctx, env, EvalParams{
		Servers:      servers,
		AllowedTools: "Read",
	}, reviewPRsTask)
	if err != nil {
		return EvalResult{}, []error{err}
	}
	LogEval(logWriter(), "Review PRs (list → read files → comment)", result)
	return result, assertReviewResult(result)
}

func assertReviewResult(result EvalResult) []error {
	var errs []error
	for _, tc := range EvalWithLabels(result) {
		for i, run := range tc.Stats.Runs {
			errs = append(errs, assertReviewRun(RepLabel(tc.Label, i, len(tc.Stats.Runs)), run)...)
		}
	}
	return errs
}

func assertReviewRun(label string, run ClaudeResult) []error {
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("[%s] %w", label, err))
		}
	}
	if run.Err != nil {
		add(fmt.Errorf("run failed: %w (logs: %s)", run.Err, run.CallLogDir))
		return errs
	}
	add(AssertToolCalled(run.CallLogDir, "github", "list_pull_requests"))
	add(AssertToolCalled(run.CallLogDir, "github", "get_file_contents"))
	add(AssertToolCalled(run.CallLogDir, "github", "add_issue_comment"))
	add(AssertResponseContains(run.Text, "820", "821", "822"))
	if run.Turns < 3 {
		errs = append(errs, fmt.Errorf("[%s] expected at least 3 turns for a multi-step review, got %d", label, run.Turns))
	}
	return errs
}
