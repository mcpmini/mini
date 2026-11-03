// funclen checks that no function or method exceeds the project code-line limits.
// Warning >= 18 lines, error >= 25 lines. Comment-only lines inside the function
// body don't count, so a well-documented invariant doesn't inflate the length.
// Functions annotated with //nolint:funclen on their declaration line suppress warnings (not errors).
//
// Test files are held to the error limit per scope: each t.Run body is measured on its own, and
// table literals don't count. Tests that were already over the limit are listed in the baseline,
// which may only shrink.
//
// Exit code 1 when any errors are found.
//
// Usage: funclen [-prune-baseline] [-baseline FILE] [dir ...]   (default: current directory, recursive)
package main

import (
	"flag"
	"fmt"
	"go/token"
	"io"
	"os"
	"strings"
)

const (
	warnAt  = 18
	errorAt = 25
)

type options struct {
	dirs          []string
	baselinePath  string
	pruneBaseline bool
}

func main() {
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	failed, err := run(opts, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if failed {
		os.Exit(1)
	}
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("funclen", flag.ContinueOnError)
	var opts options
	fs.StringVar(&opts.baselinePath, "baseline", "tools/funclen/test_baseline.txt", "tests allowed over the limit")
	fs.BoolVar(&opts.pruneBaseline, "prune-baseline", false, "lower or remove baseline entries for tests that shrank")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	opts.dirs = fs.Args()
	if len(opts.dirs) == 0 {
		opts.dirs = []string{"."}
	}
	return opts, nil
}

func run(opts options, out io.Writer) (failed bool, err error) {
	files, err := collectFiles(opts.dirs)
	if err != nil {
		return false, err
	}
	fset := token.NewFileSet()
	issues := checkProduction(files.production, fset)
	problems, err := checkTestFiles(files.tests, fset, opts)
	if err != nil {
		return false, err
	}
	var report strings.Builder
	for _, iss := range issues {
		report.WriteString(iss.String() + "\n")
		failed = failed || iss.isError
	}
	for _, p := range problems {
		report.WriteString("ERROR " + p + "\n")
	}
	_, err = io.WriteString(out, report.String())
	return failed || len(problems) > 0, err
}

func checkTestFiles(paths []string, fset *token.FileSet, opts options) ([]string, error) {
	b, err := loadBaseline(opts.baselinePath)
	if err != nil {
		return nil, err
	}
	funcs, err := measureTests(paths, fset)
	if err != nil {
		return nil, err
	}
	inRun := runScope(opts.dirs)
	if opts.pruneBaseline {
		b.prune(funcs, inRun)
		if err := b.write(); err != nil {
			return nil, err
		}
	}
	return b.check(funcs, inRun), nil
}
