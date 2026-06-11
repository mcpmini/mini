//go:build evals

package evals

import (
	"context"
	"fmt"
)

const sprintPlanTask = "Check Linear for open issues, look at recent GitHub pull requests and search for any open bugs in Jira. Then produce a brief sprint plan: list the 3 highest-priority items with a one-sentence description of each and why it matters."

// RunSprintPlanningEval exercises a multi-system planning workflow: Linear + GitHub + Jira.
func RunSprintPlanningEval(ctx context.Context, r *Runner, env *Env) (EvalResult, []error) {
	servers, err := DefaultServers(r, env, "linear", "github", "jira")
	if err != nil {
		return EvalResult{}, []error{err}
	}
	result, err := r.RunEval(ctx, evalCtx{Env: env, Params: EvalParams{Servers: servers}, Task: sprintPlanTask})
	if err != nil {
		return EvalResult{}, []error{err}
	}
	LogEval(logWriter(), "Sprint planning (Linear + GitHub + Jira)", result)
	return result, assertSprintResult(result)
}

func assertSprintResult(result EvalResult) []error {
	var errs []error
	for _, tc := range EvalWithLabels(result) {
		for i, run := range tc.Stats.Runs {
			errs = append(errs, assertSprintRun(RepLabel(tc.Label, i, len(tc.Stats.Runs)), run)...)
		}
	}
	return errs
}

func assertSprintRun(label string, run ClaudeResult) []error {
	var errs []error
	add := labeledAdder(label, &errs)
	if run.Err != nil {
		add(fmt.Errorf("run failed: %w (logs: %s)", run.Err, run.CallLogDir))
		return errs
	}
	add(AssertToolCalled(run.CallLogDir, "linear", "list_issues"))
	add(AssertToolCalled(run.CallLogDir, "github", "list_pull_requests"))
	add(AssertToolCalled(run.CallLogDir, "jira", "search_issues"))
	add(AssertResponseContains(run.Text, "ENG-", "WEBAPP-"))
	if run.Turns < 3 {
		errs = append(errs, fmt.Errorf("[%s] expected at least 3 turns for a multi-system task, got %d", label, run.Turns))
	}
	return errs
}

// RunBaselineEval measures token overhead from tool schema loading alone (no tool calls).
func RunBaselineEval(ctx context.Context, r *Runner, env *Env) (EvalResult, []error) {
	servers, err := DefaultServers(r, env, "github", "sentry", "slack", "jira", "linear")
	if err != nil {
		return EvalResult{}, []error{err}
	}
	result, err := r.RunEval(ctx, evalCtx{Env: env, Params: EvalParams{Servers: servers},
		Task: "Say hello and nothing else. Do not use any tools."})
	if err != nil {
		return EvalResult{}, []error{err}
	}
	LogEval(logWriter(), "Token baseline (all servers, no tool calls)", result)
	var errs []error
	if result.Direct.Ran() && len(result.Direct.Runs) > 0 && result.Direct.Runs[0].Text == "" {
		errs = append(errs, fmt.Errorf("[raw] expected non-empty response"))
	}
	return result, errs
}
