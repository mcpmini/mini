package server

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// expandEnv replaces $VAR or ${VAR} references with environment values.
func expandEnv(s string) string {
	return os.Expand(s, os.Getenv)
}

func applyToolTimeout(ctx context.Context, spec string) (context.Context, context.CancelFunc) {
	if spec == "" {
		spec = "30s"
	}
	if spec == "0" {
		return ctx, func() {}
	}
	d, err := time.ParseDuration(spec)
	if err != nil || d <= 0 {
		// Unparseable spec means no timeout; log so operators can spot misconfiguration.
		slog.Warn("invalid tool_timeout spec, no timeout applied", "spec", spec)
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}
