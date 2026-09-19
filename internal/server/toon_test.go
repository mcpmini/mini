package server

import (
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/response"
)

func mustEncodeToon(t *testing.T, env *response.Envelope) string {
	t.Helper()
	out, err := EncodeToon(discardLogger(), env)
	if err != nil {
		t.Fatalf("EncodeToon failed: %v", err)
	}
	return out
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestEncodeToon(t *testing.T) {
	t.Run("uniform array renders a tabular block", func(t *testing.T) {
		env := &response.Envelope{Data: []any{
			map[string]any{"id": float64(1), "name": "alice"},
			map[string]any{"id": float64(2), "name": "bob"},
		}}
		out := mustEncodeToon(t, env)
		if !strings.HasPrefix(out, "data[2]{id,name}:") {
			t.Fatalf("expected tabular block, got: %s", out)
		}
	})

	t.Run("scalar data renders inline", func(t *testing.T) {
		out := mustEncodeToon(t, &response.Envelope{Data: "hello"})
		if out != "data: hello" {
			t.Errorf("got %q", out)
		}
	})

	t.Run("error envelope renders through the same toon path, no special ERROR format", func(t *testing.T) {
		env := &response.Envelope{Error: "tool_error", Message: "boom"}
		out := mustEncodeToon(t, env)
		if !strings.Contains(out, "error: tool_error") || !strings.Contains(out, "message: boom") {
			t.Errorf("expected error fields rendered as ordinary toon fields, got: %s", out)
		}
	})

	t.Run("file field carries the recovery key, no header line", func(t *testing.T) {
		key := "1750830563123"
		env := &response.Envelope{Data: "ok", File: &key}
		out := mustEncodeToon(t, env)
		if strings.HasPrefix(out, "[") {
			t.Errorf("expected no [server.tool] header, got: %s", out)
		}
		if !strings.Contains(out, "file:") || !strings.Contains(out, key) {
			t.Errorf("expected file field with recovery key %q, got: %s", key, out)
		}
	})

	t.Run("non-finite float data returns an error", func(t *testing.T) {
		cases := []struct {
			name string
			data any
		}{
			{"NaN", math.NaN()},
			{"+Inf", math.Inf(1)},
			{"-Inf", math.Inf(-1)},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				out, err := EncodeToon(discardLogger(), &response.Envelope{Data: tc.data})
				if err == nil {
					t.Fatalf("EncodeToon(%s) succeeded, want error", tc.name)
				}
				if out != "" {
					t.Errorf("EncodeToon(%s) returned output on error: %q", tc.name, out)
				}
			})
		}
	})

	t.Run("finite struct data keeps encoding/json semantics", func(t *testing.T) {
		type item struct {
			A float64 `json:"a"`
			B string  `json:"b,omitempty"`
		}
		got := mustEncodeToon(t, &response.Envelope{Data: item{A: 1}})
		if strings.Contains(got, "b") {
			t.Errorf("omitempty was lost: %s", got)
		}
		if !strings.Contains(got, "a: 1") {
			t.Errorf("expected finite struct field encoded, got: %s", got)
		}
	})
}

func TestEncodeToonRejectsDepthLimit(t *testing.T) {
	t.Run("depth cap is returned to the caller", func(t *testing.T) {
		nested := map[string]any{"leaf": "value"}
		for i := 0; i < 1025; i++ {
			nested = map[string]any{"level": nested, "other": i}
		}
		out, err := EncodeToon(discardLogger(), &response.Envelope{Data: nested})
		if out != "" {
			t.Errorf("failed TOON encode must not return partial output: %q", out)
		}
		if err == nil || !strings.Contains(err.Error(), "nesting depth exceeds") {
			t.Errorf("expected depth error, got: %v", err)
		}
	})
}

func TestEncodeToonRejectsSizeLimit(t *testing.T) {
	const entryCount = 110000
	longVal := strings.Repeat("x", 30) // keyed tabular compresses rows; longer values ensure 4MB cap is hit
	entries := make(map[string]any, entryCount)
	for i := 0; i < entryCount; i++ {
		entries[fmt.Sprintf("k%d", i)] = map[string]any{"a": i, "b": longVal}
	}
	data := map[string]any{"entries": entries}

	out, err := EncodeToon(discardLogger(), &response.Envelope{Data: data})
	if out != "" {
		t.Errorf("failed TOON encode must not return partial output: %q", out[:min(len(out), 50)])
	}
	if err == nil || !strings.Contains(err.Error(), "encoded output exceeds") {
		t.Errorf("expected size cap error, got: %v", err)
	}
}
