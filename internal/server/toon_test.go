package server

import (
	"bytes"
	"io"
	"log/slog"
	"math"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/response"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestEncodeToon(t *testing.T) {
	t.Run("uniform array renders a tabular block", func(t *testing.T) {
		env := &response.Envelope{Data: []any{
			map[string]any{"id": float64(1), "name": "alice"},
			map[string]any{"id": float64(2), "name": "bob"},
		}}
		out := EncodeToon(discardLogger(), env)
		if !strings.HasPrefix(out, "data[2]{id,name}:") {
			t.Fatalf("expected tabular block, got: %s", out)
		}
	})

	t.Run("scalar data renders inline", func(t *testing.T) {
		out := EncodeToon(discardLogger(), &response.Envelope{Data: "hello"})
		if out != "data: hello" {
			t.Errorf("got %q", out)
		}
	})

	t.Run("error envelope renders through the same toon path, no special ERROR format", func(t *testing.T) {
		env := &response.Envelope{Error: "tool_error", Message: "boom"}
		out := EncodeToon(discardLogger(), env)
		if !strings.Contains(out, "error: tool_error") || !strings.Contains(out, "message: boom") {
			t.Errorf("expected error fields rendered as ordinary toon fields, got: %s", out)
		}
	})

	t.Run("file field carries the recovery key, no header line", func(t *testing.T) {
		key := "1750830563123"
		env := &response.Envelope{Data: "ok", File: &key}
		out := EncodeToon(discardLogger(), env)
		if strings.HasPrefix(out, "[") {
			t.Errorf("expected no [server.tool] header, got: %s", out)
		}
		if !strings.Contains(out, "file:") || !strings.Contains(out, key) {
			t.Errorf("expected file field with recovery key %q, got: %s", key, out)
		}
	})

	t.Run("non-finite float data normalizes to null, not a JSON fallback", func(t *testing.T) {
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
				var logBuf bytes.Buffer
				logger := slog.New(slog.NewTextHandler(&logBuf, nil))
				out := EncodeToon(logger, &response.Envelope{Data: tc.data})
				if logBuf.Len() > 0 {
					t.Errorf("expected no warning log for normalized float, got: %s", logBuf.String())
				}
				if out != "data: null" {
					t.Errorf("EncodeToon(%s) = %q, want \"data: null\"", tc.name, out)
				}
			})
		}
	})

	t.Run("nested non-finite values are normalized, siblings preserved", func(t *testing.T) {
		// JSON-sourced upstream data never contains non-finite floats (JSON does
		// not represent them); this normalization covers Go-constructed envelopes
		// such as config, status, and action responses.
		data := map[string]any{
			"a":    1.5,
			"bad":  math.Inf(1),
			"list": []any{1.0, math.NaN()},
		}
		out := EncodeToon(discardLogger(), &response.Envelope{Data: data})
		if strings.HasPrefix(out, "{") {
			t.Fatalf("expected TOON output, got JSON: %s", out)
		}
		if strings.Contains(out, "Inf") || strings.Contains(out, "NaN") {
			t.Errorf("non-finite values must not appear in TOON output: %s", out)
		}
		if !strings.Contains(out, "a: 1.5") {
			t.Errorf("finite sibling 'a' must be preserved: %s", out)
		}
	})

	t.Run("non-finite in plain struct exported field is normalized", func(t *testing.T) {
		type payload struct {
			Name  string  `json:"name"`
			Score float64 `json:"score"`
		}
		out := EncodeToon(discardLogger(), &response.Envelope{Data: payload{Name: "ok", Score: math.NaN()}})
		if strings.HasPrefix(out, "{") {
			t.Fatalf("expected TOON output, got JSON: %s", out)
		}
		if !strings.Contains(out, "score: null") {
			t.Errorf("non-finite struct field must be null: %s", out)
		}
		if !strings.Contains(out, "name: ok") {
			t.Errorf("finite struct field must be preserved: %s", out)
		}
	})

	t.Run("non-finite in passthrough is normalized", func(t *testing.T) {
		env := &response.Envelope{
			Data:        "ok",
			Passthrough: map[string]any{"score": math.NaN(), "label": "good"},
		}
		out := EncodeToon(discardLogger(), env)
		if !strings.Contains(out, "data: ok") {
			t.Errorf("data field must be preserved: %s", out)
		}
		if !strings.Contains(out, "score: null") {
			t.Errorf("non-finite passthrough value must be null: %s", out)
		}
		if !strings.Contains(out, "label: good") {
			t.Errorf("finite passthrough value must be preserved: %s", out)
		}
	})

	t.Run("normalization is a no-op on finite data", func(t *testing.T) {
		env := &response.Envelope{Data: []any{
			map[string]any{"id": float64(1), "name": "alice"},
			map[string]any{"id": float64(2), "name": "bob"},
		}}
		want := EncodeToon(discardLogger(), env)
		got := EncodeToon(discardLogger(), env)
		if got != want {
			t.Errorf("repeated EncodeToon on finite data differs: want %q got %q", want, got)
		}
		if !strings.HasPrefix(got, "data[2]{id,name}:") {
			t.Errorf("finite data must still produce tabular TOON: %s", got)
		}
	})

	t.Run("finite struct data keeps encoding/json semantics untouched", func(t *testing.T) {
		type item struct {
			A float64 `json:"a"`
			B string  `json:"b,omitempty"`
		}
		got := EncodeToon(discardLogger(), &response.Envelope{Data: item{A: 1}})
		if strings.Contains(got, "b") {
			t.Errorf("omitempty was lost — normalizer must not run on finite data: %s", got)
		}
		if !strings.Contains(got, "a: 1") {
			t.Errorf("expected finite struct field encoded, got: %s", got)
		}
	})

	t.Run("caller envelope map is not mutated by EncodeToon", func(t *testing.T) {
		m := map[string]any{"bad": math.NaN(), "ok": 1.0}
		env := &response.Envelope{Data: m}
		EncodeToon(discardLogger(), env)
		v, isFloat := m["bad"].(float64)
		if !isFloat || !math.IsNaN(v) {
			t.Errorf("original map was mutated: m[\"bad\"] = %v", m["bad"])
		}
	})
}
