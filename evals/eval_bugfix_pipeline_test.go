//go:build evals

package evals_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const bugFixPipelineTask = "Find the highest-priority open bug in Jira, look it up in Sentry for error details, fix it in the codebase, and open a GitHub pull request referencing the Jira ticket."

// TestEvalBugFixPipeline is the golden eval: it exercises the full agent
// bug-fix loop across Jira, Sentry, code changes, and a GitHub PR.
func TestEvalBugFixPipeline(t *testing.T) {
	r := runBugFixPipelineEval(t)
	for _, tc := range evalWithLabels(r) {
		assertBugFixPipelineMode(t, tc.label, tc.result)
	}
}

func runBugFixPipelineEval(t *testing.T) EvalResult {
	t.Helper()
	r := runEval(t, evalParams{
		servers:      defaultServers(t, "jira", "sentry", "github"),
		allowedTools: "Read,Edit,Write",
		workSrcDir:   bugfixTestdataDir(),
	}, bugFixPipelineTask)
	logEval(t, "Bug fix pipeline (Jira → Sentry → code fix → GitHub PR)", r)
	return r
}

func assertBugFixPipelineMode(t *testing.T, mode string, result ClaudeResult) {
	t.Helper()
	t.Logf("[%s] raw output: %s", mode, result.RawOutputPath)
	if result.Text == "" {
		t.Logf("[%s] skipping assertions: run did not produce output (rate limit or timeout)", mode)
		return
	}
	assertToolCalled(t, result.CallLogDir, "jira", "search_issues")
	assertServerCalled(t, result.CallLogDir, "sentry")
	assertToolCalled(t, result.CallLogDir, "github", "create_pull_request")
	verifyBugFix(t, mode, result.WorkDir)
	assertResponseContains(t, mode, result.Text, "WEBAPP-441", "JWT")
	if result.Turns < 4 {
		t.Errorf("[%s] expected at least 4 turns for the full pipeline, got %d", mode, result.Turns)
	}
}

func bugfixTestdataDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "testdata", "bugfix")
}

func verifyBugFix(t *testing.T, mode, workDir string) {
	t.Helper()
	if workDir == "" {
		return
	}
	content, err := os.ReadFile(filepath.Join(workDir, "response", "storage.go"))
	if err != nil {
		t.Errorf("[%s] could not read response/storage.go: %v", mode, err)
		return
	}
	hasGuard := bytes.Contains(content, []byte("budget")) ||
		bytes.Contains(content, []byte("usedBytes")) ||
		bytes.Contains(content, []byte("quota"))
	if !hasGuard {
		t.Errorf("[%s] response/storage.go does not appear to include a disk-space guard", mode)
	}
}
