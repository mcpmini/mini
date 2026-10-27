package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type check struct {
	ID           string `json:"id"`
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

func problemChecks(data []byte) ([]check, error) {
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
	return problems, nil
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
	if !o.Ran {
		summary.WriteString("The suite saved no results: it skipped the scenario or failed before running it. " +
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
