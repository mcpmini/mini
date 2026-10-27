//go:build test

package main

import (
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

func TestRenderSummaryListsOnlyFailures(t *testing.T) {
	summary := renderSummary([]outcome{
		{Set: "agent", Scenario: "tools_call"},
		{Set: "auth", Scenario: "auth/resource-mismatch", ExitCode: 1, Problems: []check{
			{ID: "resource-mismatch-rejected", Status: "FAILURE", Description: "Client MUST validate"},
		}},
		{Set: "cli", Scenario: "request-metadata", ExitCode: 1},
	})
	for _, want := range []string{
		"1 of 3 scenarios passed.",
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
