package jq

import (
	"bytes"
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

// Eval runs a jq filter against JSON data. Multiple results are newline-separated.
// Numbers are preserved at full precision; HTML-special characters (<, >, &) are not escaped.
// Output is capped at 4 MB / 10 000 results; context deadline bounds evaluation time.
// For fields larger than 4 MB, omit filter to retrieve the whole file.
func Eval(ctx context.Context, data []byte, filter string) (string, error) {
	q, err := gojq.Parse(filter)
	if err != nil {
		return "", fmt.Errorf("invalid jq filter %q: %w", filter, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("not valid JSON: %w", err)
	}
	return runQuery(ctx, q, v)
}

func runQuery(ctx context.Context, q *gojq.Query, v any) (string, error) {
	// agent filters must not read process env vars — WithEnvironLoader blocks $ENV/env access
	code, err := gojq.Compile(q, gojq.WithEnvironLoader(func() []string { return nil }))
	if err != nil {
		return "", fmt.Errorf("jq compile: %w", err)
	}
	iter := code.RunWithContext(ctx, v)
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

func marshalJSON(out any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("jq marshal: %w", err)
	}
	b := buf.Bytes()
	return b[:len(b)-1], nil // trim trailing newline that Encode appends
}

func writeResult(b *strings.Builder, out any, index int) error {
	if index >= maxOutputResults {
		return fmt.Errorf("jq: output exceeded %d results", maxOutputResults)
	}
	line, err := marshalJSON(out)
	if err != nil {
		return err
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
