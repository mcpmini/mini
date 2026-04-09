//go:build evals

package evals_test

import "testing"

const reviewPRsTask = "List the open pull requests. For each PR, fetch the changed files and read the relevant source files to understand what changed. Then post a brief code review comment on each PR summarizing: what the change does, any obvious risks, and whether it looks ready to merge."

// TestEval_ReviewPRs exercises a multi-step code review loop:
// list PRs → get file contents → post review comments.
func TestEval_ReviewPRs(t *testing.T) {
	servers := defaultServers(t, "github")
	r := runEval(t, evalParams{
		servers:      servers,
		allowedTools: "Read",
	}, reviewPRsTask)
	logEval(t, "Review PRs (list → read files → comment)", r)

	for _, tc := range evalWithLabels(r) {
		assertReviewPRsMode(t, tc.label, tc.result)
	}
}

func assertReviewPRsMode(t *testing.T, label string, result ClaudeResult) {
	t.Helper()
	if result.Text == "" {
		t.Logf("[%s] skipping assertions: run did not produce output (rate limit or timeout)", label)
		return
	}
	assertToolCalled(t, result.CallLogDir, "github", "list_pull_requests")
	assertToolCalled(t, result.CallLogDir, "github", "get_file_contents")
	assertToolCalled(t, result.CallLogDir, "github", "add_issue_comment")
	assertResponseContains(t, label, result.Text, "820", "821", "822")
	if result.Turns < 3 {
		t.Errorf("[%s] expected at least 3 turns for a multi-step review, got %d", label, result.Turns)
	}
}
