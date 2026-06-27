package server

import (
	"context"
	"log/slog"
	"time"
)

func applyToolTimeout(ctx context.Context, spec string) (context.Context, context.CancelFunc) {
	d, ok := parseToolTimeout(spec)
	if !ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func parseToolTimeout(spec string) (time.Duration, bool) {
	if spec == "" {
		return 30 * time.Second, true
	}
	if spec == "0" {
		return 0, false
	}
	d, err := time.ParseDuration(spec)
	if err != nil || d <= 0 {
		slog.Warn("invalid tool_timeout spec, no timeout applied", "spec", spec)
		return 0, false
	}
	return d, true
}
