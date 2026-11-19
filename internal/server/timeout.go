package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

func applyToolTimeout(ctx context.Context, spec string) (context.Context, context.CancelFunc) {
	d, ok := parseToolTimeout(spec)
	if !ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func parseToolTimeout(spec string) (time.Duration, bool) {
	ts, err := config.ParseTimeoutSpec(spec, 30*time.Second)
	if err != nil {
		slog.Warn("invalid tool_timeout spec, no timeout applied", "spec", spec)
		return 0, false
	}
	return ts.Duration, ts.Enabled
}
