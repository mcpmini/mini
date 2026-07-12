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

// applyConnectTimeout bounds the startup handshake with an upstream (subprocess
// spawn, initialize, first tools/list) so a hung upstream can't block startup
// forever. Config load already rejects unparseable connect_timeout specs; the
// fallback here only matters for runtime add_server, where a bad spec should
// still get a deadline rather than silently waiting forever.
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
