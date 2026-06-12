// Package jqfilter runs jq expressions against JSON values, so agents can
// extract a slice of a large response file instead of reading it whole.
package jqfilter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/itchyny/gojq"
)

const timeout = 5 * time.Second

// Run evaluates expr against data and returns each yielded result as
// compact JSON joined with "\n", matching jq -c output. An empty string is
// returned if expr yields no results.
func Run(ctx context.Context, data any, expr string) (string, error) {
	query, err := gojq.Parse(expr)
	if err != nil {
		return "", fmt.Errorf("parse filter %q: %w", expr, err)
	}
	code, err := gojq.Compile(query)
	if err != nil {
		return "", fmt.Errorf("compile filter %q: %w", expr, err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return collectResults(ctx, code, data)
}

func collectResults(ctx context.Context, code *gojq.Code, data any) (string, error) {
	iter := code.RunWithContext(ctx, data)
	var lines []string
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			return "", explainIterError(ctx, err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshal filter result: %w", err)
		}
		lines = append(lines, string(b))
	}
	return strings.Join(lines, "\n"), nil
}

func explainIterError(ctx context.Context, err error) error {
	var halt *gojq.HaltError
	if errors.As(err, &halt) {
		return fmt.Errorf("filter halted: %v", halt.Value())
	}
	if ctx.Err() != nil {
		return fmt.Errorf("filter timed out after %s", timeout)
	}
	return fmt.Errorf("filter error: %w", err)
}
