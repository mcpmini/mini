//go:build test

package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProblemChecksKeepsFailuresAndWarnings(t *testing.T) {
	data := []byte(`[
		{"id": "ok", "status": "SUCCESS", "description": "fine"},
		{"id": "note", "status": "INFO", "description": "request seen"},
		{"id": "scope", "status": "WARNING", "description": "SHOULD use scope"},
		{"id": "resource", "status": "FAILURE", "description": "MUST validate", "errorMessage": "proceeded"}
	]`)
	got, err := problemChecks(data)
	if err != nil {
		t.Fatalf("problemChecks() error = %v", err)
	}
	want := []check{
		{ID: "scope", Status: "WARNING", Description: "SHOULD use scope"},
		{ID: "resource", Status: "FAILURE", Description: "MUST validate", ErrorMessage: "proceeded"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("problemChecks() = %+v, want %+v", got, want)
	}
}

func TestResultsDirReadsTheSuiteOutput(t *testing.T) {
	output := []byte("Starting scenario: tools_call\nResults saved to /results/agent/tools_call-1\nChecks:\n")
	if dir, ok := resultsDir(output); !ok || dir != "/results/agent/tools_call-1" {
		t.Fatalf("resultsDir() = %q, %v; want /results/agent/tools_call-1", dir, ok)
	}
	if _, ok := resultsDir([]byte("Error: scenario not found\n")); ok {
		t.Fatal("resultsDir() found a directory in output without one")
	}
}

func TestScenarioWithoutChecksWasNotRun(t *testing.T) {
	output := []byte("Results saved to " + filepath.Join(t.TempDir(), "agent", "tools_call-1") + "\n")
	_, ran, err := scenarioProblems(output)
	if err != nil {
		t.Fatalf("scenarioProblems() error = %v", err)
	}
	if ran {
		t.Fatal("scenarioProblems() ran = true, want false when checks.json is missing")
	}
}

func TestRenderSummaryListsOnlyFailures(t *testing.T) {
	summary := renderSummary([]outcome{
		{Set: "agent", Scenario: "tools_call", Ran: true},
		{Set: "legacy", Scenario: "sep-2322-client-request-state"},
		{Set: "auth", Scenario: "auth/resource-mismatch", ExitCode: 1, Ran: true, Problems: []check{
			{ID: "resource-mismatch-rejected", Status: "FAILURE", Description: "Client MUST validate"},
		}},
		{Set: "cli", Scenario: "request-metadata", ExitCode: 1, Ran: true},
	})
	for _, want := range []string{
		"1 of 4 scenarios passed.",
		"### legacy: sep-2322-client-request-state (exit 0)",
		"The suite saved no results",
		"### auth: auth/resource-mismatch (exit 1)",
		"- FAILURE `resource-mismatch-rejected`: Client MUST validate",
		"### cli: request-metadata (exit 1)",
		"the adapter or mini errored",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary is missing %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "tools_call") {
		t.Errorf("summary lists a passing scenario:\n%s", summary)
	}
}

func TestRunnerRejectsWarningChecks(t *testing.T) {
	result := outcome{
		Set: "auth", Scenario: "auth/scope-from-scopes-supported", Ran: true,
		Problems: []check{{ID: "scope", Status: "WARNING", Description: "SHOULD use scope"}},
	}
	if !anyFailed([]outcome{result}) {
		t.Fatal("runner passed a scenario with a warning and suite exit code zero")
	}
	summary := renderSummary([]outcome{result})
	for _, want := range []string{"0 of 1 scenarios passed", "WARNING `scope`: SHOULD use scope"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary is missing %q:\n%s", want, summary)
		}
	}
}
