//go:build test

package server

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/response"
)

func TestFormatEnvelopeReturnsToonEncodingLimit(t *testing.T) {
	nested := map[string]any{"leaf": "value"}
	for i := 0; i < 70; i++ {
		nested = map[string]any{"level": nested, "other": i}
	}

	srv := &Server{
		cfg:    config.DefaultConfig(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	srv.cfg.ResponseFormat = config.FormatToon
	result, err := srv.formatEnvelope("gh", "list_issues", &response.Envelope{Data: nested}, nil)
	if result != nil {
		t.Fatalf("failed TOON encoding returned a response: %#v", result)
	}
	if err == nil || !strings.Contains(err.Error(), "encode TOON response") {
		t.Fatalf("expected TOON encoding error, got: %v", err)
	}
}
