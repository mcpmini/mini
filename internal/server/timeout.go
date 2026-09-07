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

const defaultHandshakeTimeout = 30 * time.Second

// Config load rejects unparseable handshake_timeout specs; the fallback to the default
// here only matters for runtime add_server, which bypasses that validation.
func applyHandshakeTimeout(ctx context.Context, spec string) (context.Context, context.CancelFunc) {
	ts, err := config.ParseTimeoutSpec(spec, defaultHandshakeTimeout)
	if err != nil {
		slog.Warn("invalid handshake_timeout spec, using default", "spec", spec, "default", defaultHandshakeTimeout)
		ts = config.TimeoutSpec{Duration: defaultHandshakeTimeout, Enabled: true}
	}
	if !ts.Enabled {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, ts.Duration)
}
