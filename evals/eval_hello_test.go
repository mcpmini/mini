//go:build evals

package evals_test

import "testing"

const sprintPlanTask = "Check Linear for open issues, look at recent GitHub pull requests and search for any open bugs in Jira. Then produce a brief sprint plan: list the 3 highest-priority items with a one-sentence description of each and why it matters."

// TestEval_TokenBaseline measures token overhead from tool schema loading alone
// (no tool calls). Used to quantify the fixed cost of listing many servers.
func TestEval_TokenBaseline(t *testing.T) {
	servers := defaultServers(t, "github", "sentry", "slack", "jira", "linear")
	r := runTriple(t, evalParams{servers: servers}, "Say hello and nothing else. Do not use any tools.")
	logTriple(t, "Token baseline (all servers, no tool calls)", r)
	if r.Raw.Text == "" {
		t.Fatal("expected non-empty response")
	}
}

// TestEval_SprintPlanning exercises a multi-system planning workflow:
// Linear issues + GitHub PRs + Jira bugs → prioritized sprint plan.
func TestEval_SprintPlanning(t *testing.T) {
	servers := defaultServers(t, "linear", "github", "jira")
	r := runTriple(t, evalParams{servers: servers}, sprintPlanTask)
	logTriple(t, "Sprint planning (Linear + GitHub + Jira)", r)

	for _, tc := range tripleWithLabels(r) {
		assertSprintPlanningMode(t, tc.label, tc.result)
	}
}

func assertSprintPlanningMode(t *testing.T, label string, result ClaudeResult) {
	t.Helper()
	assertToolCalled(t, result.CallLogDir, "linear", "list_issues")
	assertToolCalled(t, result.CallLogDir, "github", "list_pull_requests")
	assertToolCalled(t, result.CallLogDir, "jira", "search_issues")
	// Response should reference content from the fixtures (Linear ENG IDs or Jira WEBAPP IDs)
	assertResponseContains(t, label, result.Text, "ENG-", "WEBAPP-")
	if result.Text == "" {
		t.Errorf("[%s] expected non-empty response", label)
	}
	if result.Turns < 3 {
		t.Errorf("[%s] expected at least 3 turns for a multi-system task, got %d", label, result.Turns)
	}
}
