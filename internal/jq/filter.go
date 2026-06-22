package jq

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itchyny/gojq"
)

// Eval runs a jq filter against JSON data and returns the output.
// Multiple results are newline-separated, matching jq stream semantics.
// An empty result set returns "". An invalid filter returns an error.
func Eval(ctx context.Context, data []byte, filter string) (string, error) {
	q, err := gojq.Parse(filter)
	if err != nil {
		return "", fmt.Errorf("invalid jq filter %q: %w", filter, err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return "", fmt.Errorf("not valid JSON: %w", err)
	}
	return runQuery(ctx, q, v)
}

func runQuery(ctx context.Context, q *gojq.Query, v any) (string, error) {
	iter := q.RunWithContext(ctx, v)
	var b strings.Builder
	first := true
	for {
		out, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := out.(error); ok {
			return "", fmt.Errorf("jq: %w", err)
		}
		if !first {
			b.WriteByte('\n')
		}
		first = false
		line, merr := json.Marshal(out)
		if merr != nil {
			return "", fmt.Errorf("jq marshal: %w", merr)
		}
		b.Write(line)
	}
	return b.String(), nil
}
