//go:build evals

// run-eval launches eval runs with explicit mode and repetition control.
//
// Usage:
//
//	run-eval <eval|all> [mode...] [--reps N]
//
// Evals:  bugfix | review-prs | incident-triage | sprint | baseline
// Modes:  raw | mcp-passthrough | mcp-projected | mcp-lines |
//
//	cli-passthrough | cli-projected | cli-lines
//
// Examples:
//
//	run-eval bugfix                       # all 7 modes, 1 rep
//	run-eval bugfix raw                   # raw only
//	run-eval bugfix raw --reps 5          # raw, 5 reps each
//	run-eval all raw --reps 3             # all evals, raw mode, 3 reps
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mcpmini/mini/evals"
)

type evalEntry struct {
	name string
	fn   func(context.Context, *evals.Runner, *evals.Env) (evals.EvalResult, []error)
}

var registry = []evalEntry{
	{"bugfix", evals.RunBugfixEval},
	{"review-prs", evals.RunReviewPRsEval},
	{"incident-triage", evals.RunIncidentTriageEval},
	{"sprint", evals.RunSprintPlanningEval},
	{"baseline", evals.RunBaselineEval},
}

var validModes = map[string]bool{
	"direct": true, "mcp-passthrough": true, "mcp-projected": true, "mcp-lines": true,
	"cli-passthrough": true, "cli-projected": true, "cli-lines": true,
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, usage())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing eval name")
	}
	evalArg := args[0]
	reps := 1
	var modes []string
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--reps" && i+1 < len(args):
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 {
				return fmt.Errorf("--reps must be a positive integer, got %q", args[i+1])
			}
			reps = n
			i++
		case validModes[args[i]]:
			modes = append(modes, args[i])
		default:
			return fmt.Errorf("unknown argument %q", args[i])
		}
	}

	var toRun []evalEntry
	if evalArg == "all" {
		toRun = registry
	} else {
		e, ok := findEval(evalArg)
		if !ok {
			return fmt.Errorf("unknown eval %q — valid: %s", evalArg, evalNames())
		}
		toRun = []evalEntry{e}
	}

	modeSet := parseModes(modes)
	label := evalArg
	if len(modes) > 0 {
		label += "  [" + strings.Join(modes, ", ") + "]"
	}
	if reps > 1 {
		label += fmt.Sprintf("  ×%d reps", reps)
	}
	fmt.Println("▶", label)
	fmt.Println()

	resultsFile, resultsPath, err := openResultsFile(evalArg, modes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not open results file: %v\n", err)
	} else {
		defer resultsFile.Close()
		evals.SetOutput(io.MultiWriter(os.Stdout, resultsFile))
		fmt.Fprintf(resultsFile, "▶ %s\n\n", label)
		fmt.Printf("Saving results to %s\n\n", resultsPath)
	}

	fmt.Println("Building binaries...")
	r, err := evals.NewRunner(modeSet, reps)
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	fmt.Println()

	ctx := context.Background()
	failed := false
	for _, e := range toRun {
		env := evals.NewEnv()
		_, errs := e.fn(ctx, r, env)
		env.Cleanup(len(errs) == 0)
		if len(errs) > 0 {
			failed = true
			fmt.Printf("✗ %s\n", e.name)
			for _, err := range errs {
				msg := fmt.Sprintf("  %v\n", err)
				fmt.Fprint(os.Stderr, msg)
				if resultsFile != nil {
					fmt.Fprint(resultsFile, msg)
				}
			}
		} else {
			fmt.Printf("✓ %s\n", e.name)
		}
		fmt.Println()
	}

	if failed {
		return fmt.Errorf("one or more evals failed")
	}
	return nil
}

func findEval(name string) (evalEntry, bool) {
	for _, e := range registry {
		if e.name == name {
			return e, true
		}
	}
	return evalEntry{}, false
}

func parseModes(modes []string) map[string]bool {
	if len(modes) == 0 {
		return nil
	}
	m := make(map[string]bool, len(modes))
	for _, s := range modes {
		m[s] = true
	}
	return m
}

func evalNames() string {
	names := make([]string, len(registry))
	for i, e := range registry {
		names[i] = e.name
	}
	return strings.Join(names, ", ")
}

func openResultsFile(evalArg string, modes []string) (*os.File, string, error) {
	root, err := evals.FindRepoRoot()
	if err != nil {
		return nil, "", err
	}
	dir := filepath.Join(root, "evals", "results")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, "", err
	}
	ts := time.Now().Format("20060102-150405")
	name := evalArg
	if len(modes) > 0 {
		name += "-" + strings.Join(modes, "+")
	}
	path := filepath.Join(dir, ts+"-"+name+".txt")
	f, err := os.Create(path)
	return f, path, err
}

func usage() string {
	return `Usage: run-eval <eval|all> [mode...] [--reps N]

Evals:  bugfix | review-prs | incident-triage | sprint | baseline
Modes:  direct | mcp-passthrough | mcp-projected | mcp-lines
        cli-passthrough | cli-projected | cli-lines`
}
