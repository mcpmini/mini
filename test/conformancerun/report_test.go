//go:build test

package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScenarioChecksKeepsFailuresAndWarnings(t *testing.T) {
	data := []byte(`[
		{"id": "ok", "status": "SUCCESS", "description": "fine"},
		{"id": "note", "status": "INFO", "description": "request seen"},
		{"id": "scope", "status": "WARNING", "description": "SHOULD use scope"},
		{"id": "resource", "status": "FAILURE", "description": "MUST validate", "errorMessage": "proceeded"}
	]`)
	got, err := scenarioChecks(data, "")
	if err != nil {
		t.Fatalf("scenarioChecks() error = %v", err)
	}
	want := []check{
		{ID: "scope", Status: "WARNING", Description: "SHOULD use scope"},
		{ID: "resource", Status: "FAILURE", Description: "MUST validate", ErrorMessage: "proceeded"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scenarioChecks() = %+v, want %+v", got, want)
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
	_, ran, err := scenarioProblems(output, "")
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

func TestToolHeaderChecksRequireObservedToolCallSuccess(t *testing.T) {
	for _, data := range []string{
		`[]`,
		`[{"id":"sep-2243-client-includes-standard-headers","name":"ClientMcpMethodHeader_tools_list","status":"SUCCESS"}]`,
		`[{"id":"sep-2243-client-includes-standard-headers","name":"ClientMcpMethodHeader_tools_call","status":"SUCCESS"},{"id":"sep-2243-client-includes-standard-headers","name":"ClientMcpNameHeader_tools_call","status":"SKIPPED"}]`,
	} {
		problems, err := scenarioChecks([]byte(data), "http-standard-headers")
		if err != nil || len(problems) == 0 {
			t.Fatalf("unexercised tool headers passed: %s; problems=%v err=%v", data, problems, err)
		}
	}
	data := []byte(
		`[{"id":"sep-2243-client-includes-standard-headers","name":"ClientMcpMethodHeader_tools_call","status":"SUCCESS"},{"id":"sep-2243-client-includes-standard-headers","name":"ClientMcpNameHeader_tools_call","status":"SUCCESS"},{"id":"sep-2243-client-includes-standard-headers","name":"ClientMcpMethodHeader_prompts_list","status":"SKIPPED"}]`,
	)
	if problems, err := scenarioChecks(data, "http-standard-headers"); err != nil || len(problems) != 0 {
		t.Fatalf("exercised tool headers failed: %v, %v", problems, err)
	}
}

func TestMetadataChecksRequireExercisedFields(t *testing.T) {
	data := []byte(`[
  {"id":"sep-2575-http-client-sends-version-header","status":"SUCCESS"},
  {"id":"sep-2575-client-populates-meta","status":"SUCCESS"},
  {"id":"sep-2575-client-sends-client-info","status":"SUCCESS"},
  {"id":"sep-2575-http-version-header-matches-meta","status":"SUCCESS"},
  {"id":"sep-2575-client-retry-supported-version","status":"SUCCESS"},
  {"id":"sep-2575-client-declares-elicitation-capability","status":"SKIPPED"}
 ]`)
	if problems, err := scenarioChecks(data, "request-metadata"); err != nil || len(problems) != 0 {
		t.Fatalf("metadata fixture with no tools failed: %v, %v", problems, err)
	}
	missing := []byte(
		strings.Replace(
			string(data),
			`"sep-2575-http-version-header-matches-meta","status":"SUCCESS"`,
			`"sep-2575-http-version-header-matches-meta","status":"SKIPPED"`,
			1,
		),
	)
	if problems, err := scenarioChecks(missing, "request-metadata"); err != nil || len(problems) != 1 {
		t.Fatalf("unexercised version/header match passed: %v, %v", problems, err)
	}
}

func TestScenarioRequiresSuccessfulCheck(t *testing.T) {
	for _, data := range []string{`[]`, `[{"id":"initialize","status":"SKIPPED"}]`, `[{"id":"info","status":"INFO"}]`} {
		problems, err := scenarioChecks([]byte(data), "initialize")
		if err != nil || len(problems) == 0 {
			t.Fatalf("unexercised scenario passed: %s; problems=%v err=%v", data, problems, err)
		}
	}
	if problems, err := scenarioChecks(
		[]byte(`[{"id":"mcp-client-initialization","status":"SUCCESS"}]`),
		"initialize",
	); err != nil ||
		len(problems) != 0 {
		t.Fatalf("exercised initialization rejected: %v %v", problems, err)
	}
}
