package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/mcpmini/mini/internal/config"
)

// ProbeServer connects to sc and lists its tools the way the proxy would, so a detected OAuth
// requirement is recorded just as it is on a real connect.
func ProbeServer(ctx context.Context, configDir string, sc config.ServerConfig) error {
	cfg, err := config.LoadMain(configDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(Params{Config: cfg, ConfigDir: configDir, Logger: logger})
	defer srv.Close()
	return srv.AddUpstream(ctx, sc)
}
