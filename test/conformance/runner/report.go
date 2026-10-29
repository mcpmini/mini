package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

func saveSummary(results string, outcomes []outcome) error {
	summary := []byte(renderSummary(outcomes))
	if err := os.WriteFile(filepath.Join(results, "summary.md"), summary, 0o600); err != nil {
		return err
	}
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		return os.WriteFile(path, summary, 0o600)
	}
	return nil
}

func report(results string, outcomes []outcome) {
	fmt.Printf("\n%s\nConformance results: %s\n", renderSummary(outcomes), results)
}
