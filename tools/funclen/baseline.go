package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
)

const pruneRemedy = "run go run ./tools/funclen -prune-baseline"

// baseline lists the tests that were over the limit when the check started, as
// "pkgdir:FuncName N" with N the longest scope allowed. It only shrinks: growth fails, and so does
// an improvement until the entry is pruned.
type baseline struct {
	path    string
	entries map[string]int
}

func loadBaseline(path string) (baseline, error) {
	b := baseline{path: path, entries: map[string]int{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return baseline{}, err
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, n, err := parseEntry(line)
		if err != nil {
			return baseline{}, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		b.entries[key] = n
	}
	return b, nil
}

func parseEntry(line string) (key string, n int, err error) {
	key, count, ok := strings.Cut(strings.TrimSpace(line), " ")
	if !ok || !strings.Contains(key, ":") {
		return "", 0, fmt.Errorf("want \"pkgdir:FuncName N\", got %q", line)
	}
	n, err = strconv.Atoi(count)
	return key, n, err
}

func (b baseline) write() error {
	keys := slices.Sorted(maps.Keys(b.entries))
	var sb strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&sb, "%s %d\n", key, b.entries[key])
	}
	return os.WriteFile(b.path, []byte(sb.String()), 0o644)
}

// prune never adds or raises an entry: that takes a hand edit, which shows in review.
func (b baseline) prune(funcs map[string]testFunc, inRun func(string) bool) {
	for key, n := range b.entries {
		if !inRun(entryPkg(key)) {
			continue
		}
		f, ok := funcs[key]
		switch {
		case !ok || f.longest.lines < errorAt:
			delete(b.entries, key)
		case f.longest.lines < n:
			b.entries[key] = f.longest.lines
		}
	}
}

func (b baseline) check(funcs map[string]testFunc, inRun func(string) bool) []string {
	var problems []string
	for _, key := range slices.Sorted(maps.Keys(funcs)) {
		if p := b.judge(key, funcs[key]); p != "" {
			problems = append(problems, p)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(b.entries)) {
		if _, ok := funcs[key]; !ok && inRun(entryPkg(key)) {
			problems = append(problems, fmt.Sprintf("%s: %s matches no function; %s", b.path, key, pruneRemedy))
		}
	}
	return problems
}

func (b baseline) judge(key string, f testFunc) string {
	n, listed := b.entries[key]
	m := f.longest.lines
	switch {
	case !listed && m >= errorAt:
		return fmt.Sprintf("%s is %d code lines (>= %d)", f.describe(), m, errorAt)
	case listed && m > n:
		return fmt.Sprintf("%s is %d code lines, baseline allows %d; "+
			"shorten it (see docs/testing.md) instead of raising the baseline", f.describe(), m, n)
	case listed && m < n:
		return fmt.Sprintf("%s is now %d lines, baseline allows %d; %s", f.describe(), m, n, pruneRemedy)
	}
	return ""
}

func entryPkg(key string) string {
	return key[:strings.LastIndex(key, ":")]
}
