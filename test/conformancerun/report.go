package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type check struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Description  string `json:"description"`
	ErrorMessage string `json:"errorMessage"`
}

func (c check) explanation() string {
	if c.ErrorMessage != "" {
		return c.ErrorMessage
	}
	return c.Description
}

func scenarioChecks(data []byte, scenario string) ([]check, error) {
	var checks []check
	if err := json.Unmarshal(data, &checks); err != nil {
		return nil, fmt.Errorf("decode checks.json: %w", err)
	}
	var problems []check
	for _, c := range checks {
		if c.Status == "FAILURE" || c.Status == "WARNING" {
			problems = append(problems, c)
		}
	}
	for _, required := range requiredChecks(scenario) {
		if !observedSuccess(checks, required) {
			problems = append(problems, check{
				ID:          required.ID,
				Name:        required.Name,
				Status:      "FAILURE",
				Description: "Required check did not succeed: " + required.ID + " " + required.Name,
			})
		}
	}
	return problems, nil
}

func requiredChecks(scenario string) []check {
	if scenario == "http-standard-headers" {
		return []check{
			{ID: "sep-2243-client-includes-standard-headers", Name: "ClientMcpMethodHeader_tools_call"},
			{ID: "sep-2243-client-includes-standard-headers", Name: "ClientMcpNameHeader_tools_call"},
		}
	}
	if scenario != "request-metadata" {
		return nil
	}
	required := []check{
		{ID: "sep-2575-http-client-sends-version-header"},
		{ID: "sep-2575-client-populates-meta"},
		{ID: "sep-2575-client-sends-client-info"},
		{ID: "sep-2575-http-version-header-matches-meta"},
		{ID: "sep-2575-client-retry-supported-version"},
	}
	return required
}

func observedSuccess(checks []check, required check) bool {
	for _, c := range checks {
		if c.ID == required.ID && (required.Name == "" || c.Name == required.Name) && c.Status == "SUCCESS" {
			return true
		}
	}
	return false
}

func resultsDir(output []byte) (string, bool) {
	for line := range strings.Lines(string(output)) {
		if dir, ok := strings.CutPrefix(line, "Results saved to "); ok {
			return strings.TrimSpace(dir), true
		}
	}
	return "", false
}

func renderSummary(outcomes []outcome) string {
	var summary strings.Builder
	passed := 0
	for _, o := range outcomes {
		if o.passed() {
			passed++
		}
	}
	fmt.Fprintf(&summary, "## MCP conformance\n\n%d of %d scenarios passed.\n", passed, len(outcomes))
	for _, o := range outcomes {
		if !o.passed() {
			writeFailure(&summary, o)
		}
	}
	return summary.String()
}

func writeFailure(summary *strings.Builder, o outcome) {
	fmt.Fprintf(summary, "\n### %s: %s (exit %d)\n\n", o.Set, o.Scenario, o.ExitCode)
	if o.Error != "" {
		fmt.Fprintf(summary, "Runner error: %s. See the scenario log.\n", o.Error)
		return
	}
	if !o.Ran {
		summary.WriteString("The suite saved no results: checks.json is missing or its location was not reported. " +
			"See the scenario's log.\n")
		return
	}
	if len(o.Problems) == 0 {
		summary.WriteString("No failed checks, so the adapter or mini errored. See the scenario's `stderr.txt`.\n")
		return
	}
	for _, p := range o.Problems {
		fmt.Fprintf(summary, "- %s `%s`: %s\n", p.Status, p.ID, p.explanation())
	}
}
