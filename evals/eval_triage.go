//go:build evals

package evals

import (
	"context"
	"fmt"
)

const incidentTriageTask = "A P1 alert just fired. Get the top critical error from Sentry. Search GitHub for the file mentioned in the Sentry culprit field. Then post exactly one message to the #incidents Slack channel containing: the Sentry issue ID, the GitHub file path, the error title, and one recommended action. Do not look at commits, PRs, or anything else."

// RunIncidentTriageEval exercises a cross-system incident workflow: Sentry → GitHub → Slack.
func RunIncidentTriageEval(ctx context.Context, r *Runner, env *Env) (EvalResult, []error) {
	servers, err := DefaultServers(r, env, "sentry", "github", "slack")
	if err != nil {
		return EvalResult{}, []error{err}
	}
	result, err := r.RunEval(ctx, evalCtx{Env: env, Params: EvalParams{Servers: servers}, Task: incidentTriageTask})
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
	add := labeledAdder(label, &errs)
	if run.Err != nil {
		add(fmt.Errorf("run failed: %w (logs: %s)", run.Err, run.CallLogDir))
		return errs
	}
	add(AssertToolCalled(run.CallLogDir, "sentry", "list_issues"))
	add(AssertToolCalled(run.CallLogDir, "github", "search_code"))
	add(AssertToolCalled(run.CallLogDir, "slack", "post_message"))
	// Fixture data is deterministic: AUTH-001 is always the top Sentry issue,
	// auth.go is always the search_code result.
	add(AssertResponseContains(run.Text, "AUTH-001", "4500000001"))
	add(AssertResponseContains(run.Text, "auth.go", "middleware.go", "auth/"))
	return errs
}
