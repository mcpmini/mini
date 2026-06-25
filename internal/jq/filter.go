package jq

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itchyny/gojq"
)

const (
	maxOutputBytes   = 4 * 1024 * 1024 // 4 MB
	maxOutputResults = 10_000
)

// Multiple results are newline-separated, matching jq stream semantics.
// An empty result set returns "". An invalid filter returns an error.
// Output is capped at 4 MB / 10 000 results to bound memory usage.
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
	for i := 0; ; i++ {
		out, ok := iter.Next()
		if !ok {
			return b.String(), nil
		}
		if err, ok := out.(error); ok {
			return "", fmt.Errorf("jq: %w", err)
		}
		if err := writeResult(&b, out, i); err != nil {
			return "", err
		}
	}
}

func writeResult(b *strings.Builder, out any, index int) error {
	if index >= maxOutputResults {
		return fmt.Errorf("jq: output exceeded %d results", maxOutputResults)
	}
	line, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("jq marshal: %w", err)
	}
	size := len(line)
	if index > 0 {
		size++ // newline separator
	}
	if b.Len()+size > maxOutputBytes {
		return fmt.Errorf("jq: output exceeded %d bytes", maxOutputBytes)
	}
	if index > 0 {
		b.WriteByte('\n')
	}
	b.Write(line)
	return nil
}
