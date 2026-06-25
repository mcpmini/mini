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
	"direct":          true,
	"mcp-passthrough": true, "mcp-projected": true, "mcp-lines": true,
	"cli-passthrough": true, "cli-projected": true, "cli-lines": true,
	"proxy-passthrough": true, "proxy-projected": true, "proxy-lines": true,
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, usage())
		os.Exit(1)
	}
}

type parsedArgs struct {
	evalArg string
	modes   []string
	reps    int
}

func run(args []string) error {
	parsed, err := parseArgs(args)
	if err != nil {
		return err
	}
	label := buildLabel(parsed.evalArg, parsed.modes, parsed.reps)
	fmt.Printf("▶ %s\n\n", label)
	resultsFile := setupResultsFile(parsed.evalArg, parsed.modes, label)
	if resultsFile != nil {
		defer resultsFile.Close()
	}
	runner, toRun, err := setupEvalRun(parsed)
	if err != nil {
		return err
	}
	return runEvals(context.Background(), runner, toRun, resultsFile)
}

func parseArgs(args []string) (parsedArgs, error) {
	if len(args) == 0 {
		return parsedArgs{}, fmt.Errorf("missing eval name")
	}
	p := parsedArgs{evalArg: args[0], reps: 1}
	for i := 1; i < len(args); i++ {
		next, err := parseArg(p, args, i)
		if err != nil {
			return parsedArgs{}, err
		}
		p = next.parsed
		i += next.skip
	}
	return p, nil
}

type parseArgResult struct {
	parsed parsedArgs
	skip   int
}

func parseArg(parsed parsedArgs, args []string, idx int) (parseArgResult, error) {
	arg := args[idx]
	switch {
	case arg == "--reps":
		n, err := parseRepsArg(args, idx)
		if err != nil {
			return parseArgResult{}, err
		}
		parsed.reps = n
		return parseArgResult{parsed: parsed, skip: 1}, nil
	case validModes[arg]:
		parsed.modes = append(parsed.modes, arg)
		return parseArgResult{parsed: parsed}, nil
	default:
		return parseArgResult{}, fmt.Errorf("unknown argument %q", arg)
	}
}

func parseRepsArg(args []string, idx int) (int, error) {
	if idx+1 >= len(args) {
		return 0, fmt.Errorf("--reps requires a value")
	}
	n, err := strconv.Atoi(args[idx+1])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("--reps must be a positive integer, got %q", args[idx+1])
	}
	return n, nil
}

func setupEvalRun(parsed parsedArgs) (*evals.Runner, []evalEntry, error) {
	toRun, err := resolveEvals(parsed.evalArg)
	if err != nil {
		return nil, nil, err
	}
	fmt.Println("Building binaries...")
	runner, err := evals.NewRunner(parseModes(parsed.modes), parsed.reps)
	if err != nil {
		return nil, nil, fmt.Errorf("setup: %w", err)
	}
	fmt.Println()
	return runner, toRun, nil
}

func resolveEvals(evalArg string) ([]evalEntry, error) {
	if evalArg == "all" {
		return registry, nil
	}
	e, ok := findEval(evalArg)
	if !ok {
		return nil, fmt.Errorf("unknown eval %q — valid: %s", evalArg, evalNames())
	}
	return []evalEntry{e}, nil
}

func buildLabel(evalArg string, modes []string, reps int) string {
	label := evalArg
	if len(modes) > 0 {
		label += "  [" + strings.Join(modes, ", ") + "]"
	}
	if reps > 1 {
		label += fmt.Sprintf("  ×%d reps", reps)
	}
	return label
}

func setupResultsFile(evalArg string, modes []string, label string) *os.File {
	f, path, err := openResultsFile(evalArg, modes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not open results file: %v\n", err)
		return nil
	}
	evals.SetOutput(io.MultiWriter(os.Stdout, f))
	fmt.Fprintf(f, "▶ %s\n\n", label)
	fmt.Printf("Saving results to %s\n\n", path)
	return f
}

func runEvals(ctx context.Context, r *evals.Runner, toRun []evalEntry, resultsFile *os.File) error {
	failed := false
	for _, e := range toRun {
		env := evals.NewEnv()
		errs := runEvalEntry(ctx, r, env, e)
		failed = reportEvalResult(e.name, errs, resultsFile) || failed
		fmt.Println()
	}
	if failed {
		return fmt.Errorf("one or more evals failed")
	}
	return nil
}

func runEvalEntry(ctx context.Context, r *evals.Runner, env *evals.Env, e evalEntry) []error {
	_, errs := e.fn(ctx, r, env)
	env.Cleanup(len(errs) == 0)
	return errs
}

func reportEvalResult(name string, errs []error, resultsFile *os.File) bool {
	if len(errs) == 0 {
		fmt.Printf("✓ %s\n", name)
		return false
	}
	printEvalErrs(name, errs, resultsFile)
	return true
}

func printEvalErrs(name string, errs []error, resultsFile *os.File) {
	fmt.Printf("✗ %s\n", name)
	for _, err := range errs {
		msg := fmt.Sprintf("  %v\n", err)
		fmt.Fprint(os.Stderr, msg)
		if resultsFile != nil {
			fmt.Fprint(resultsFile, msg)
		}
	}
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
	path := buildResultsPath(dir, evalArg, modes)
	f, err := os.Create(path)
	return f, path, err
}

func buildResultsPath(dir, evalArg string, modes []string) string {
	ts := time.Now().Format("20060102-150405")
	name := evalArg
	if len(modes) > 0 {
		name += "-" + strings.Join(modes, "+")
	}
	return filepath.Join(dir, ts+"-"+name+".txt")
}

func usage() string {
	return `Usage: run-eval <eval|all> [mode...] [--reps N]

Evals:  bugfix | review-prs | incident-triage | sprint | baseline
Modes:  direct | mcp-passthrough | mcp-projected | mcp-lines
        cli-passthrough | cli-projected | cli-lines
        proxy-passthrough | proxy-projected | proxy-lines`
}
