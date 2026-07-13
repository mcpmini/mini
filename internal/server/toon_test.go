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

	t.Run("never returns a broken response when data cannot marshal at all", func(t *testing.T) {
		// math.NaN() fails json.Marshal outright, so both the toon path and its
		// JSON fallback fail identically — this exercises the last-resort
		// fmt.Sprintf floor, proving EncodeToon still returns non-empty text
		// rather than "".
		env := &response.Envelope{Data: math.NaN()}
		var logBuf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))

		out := EncodeToon(logger, env)

		if out == "" {
			t.Fatal("expected a non-empty fallback response, got empty string")
		}
		if !strings.Contains(logBuf.String(), "toon encode failed") {
			t.Errorf("expected a WARN log on fallback, got: %s", logBuf.String())
		}
	})
}
