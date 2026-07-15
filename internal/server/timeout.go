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
	d, enabled, err := config.ParseTimeoutSpec(spec, 30*time.Second)
	if err != nil {
		slog.Warn("invalid tool_timeout spec, no timeout applied", "spec", spec)
		return 0, false
	}
	return d, enabled
}

const defaultConnectTimeout = 10 * time.Second

// Config load rejects unparseable connect_timeout specs; the fallback to the default
// here only matters for runtime add_server, which bypasses that validation.
func applyConnectTimeout(ctx context.Context, spec string) (context.Context, context.CancelFunc) {
	d, enabled, err := config.ParseTimeoutSpec(spec, defaultConnectTimeout)
	if err != nil {
		slog.Warn("invalid connect_timeout spec, using default", "spec", spec, "default", defaultConnectTimeout)
		d, enabled = defaultConnectTimeout, true
	}
	if !enabled {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}
