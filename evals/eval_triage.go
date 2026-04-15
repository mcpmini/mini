//go:build evals

package evals

import (
	"context"
	"fmt"
)

const incidentTriageTask = "We have a production incident. Check Sentry for recent errors and identify the most critical one. Search GitHub for the relevant code that's failing and look at recent commits or PRs that might have introduced it. Then post a message to the #incidents channel in Slack with: the error summary, the likely root cause based on your code investigation, and recommended next steps."

// RunIncidentTriageEval exercises a cross-system incident workflow: Sentry → GitHub → Slack.
func RunIncidentTriageEval(ctx context.Context, r *Runner, env *Env) (EvalResult, []error) {
	servers, err := DefaultServers(r, env, "sentry", "github", "slack")
	if err != nil {
		return EvalResult{}, []error{err}
	}
	result, err := r.RunEval(ctx, env, EvalParams{Servers: servers}, incidentTriageTask)
	if err != nil {
		return EvalResult{}, []error{err}
	}
	LogEval(logWriter(), "Incident triage (Sentry → GitHub → Slack)", result)
	return result, assertTriageResult(result)
}

func assertTriageResult(result EvalResult) []error {
	var errs []error
	for _, tc := range EvalWithLabels(result) {
		for i, run := range tc.Stats.Runs {
			errs = append(errs, assertTriageRun(RepLabel(tc.Label, i, len(tc.Stats.Runs)), run)...)
		}
	}
	return errs
}

func assertTriageRun(label string, run ClaudeResult) []error {
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
	add(AssertToolCalled(run.CallLogDir, "sentry", "list_issues"))
	add(AssertToolCalled(run.CallLogDir, "github", "search_code"))
	add(AssertToolCalled(run.CallLogDir, "slack", "post_message"))
	add(AssertResponseContains(run.Text, "auth", "JWT"))
	if run.Turns < 3 {
		errs = append(errs, fmt.Errorf("[%s] expected at least 3 turns for a cross-system task, got %d", label, run.Turns))
	}
	return errs
}
