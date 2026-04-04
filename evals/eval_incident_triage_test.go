//go:build evals

package evals_test

import "testing"

const incidentTriageTask = "We have a production incident. Check Sentry for recent errors and identify the most critical one. Search GitHub for the relevant code that's failing and look at recent commits or PRs that might have introduced it. Then post a message to the #incidents channel in Slack with: the error summary, the likely root cause based on your code investigation, and recommended next steps."

// TestEval_IncidentTriage exercises a cross-system incident workflow:
// Sentry errors → GitHub code/PR search → Slack notification.
func TestEval_IncidentTriage(t *testing.T) {
	servers := defaultServers(t, "sentry", "github", "slack")
	r := runTriple(t, evalParams{servers: servers}, incidentTriageTask)
	logTriple(t, "Incident triage (Sentry → GitHub → Slack)", r)

	for _, tc := range tripleWithLabels(r) {
		assertIncidentTriageMode(t, tc.label, tc.result)
	}
}

func assertIncidentTriageMode(t *testing.T, label string, result ClaudeResult) {
	t.Helper()
	assertToolCalled(t, result.CallLogDir, "sentry", "list_issues")
	assertServerCalled(t, result.CallLogDir, "github")
	assertToolCalled(t, result.CallLogDir, "slack", "post_message")
	// Response should mention the auth errors that dominate the Sentry fixture
	assertResponseContains(t, label, result.Text, "auth", "JWT")
	if result.Text == "" {
		t.Errorf("[%s] expected non-empty response", label)
	}
	if result.Turns < 3 {
		t.Errorf("[%s] expected at least 3 turns for a cross-system task, got %d", label, result.Turns)
	}
}
