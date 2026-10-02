package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"

	"github.com/mcpmini/mini/internal/config"
)

func loadConfigReportingBroken(configDir string, warnings io.Writer) (config.Loaded, error) {
	loaded, err := config.Load(configDir)
	if err != nil {
		return config.Loaded{}, fmt.Errorf("load config: %w", err)
	}
	for _, broken := range loaded.Broken {
		fmt.Fprintf(warnings, "warning: skipping %s: %v\n", broken.ServerName, broken.Err)
	}
	return loaded, nil
}

func logBrokenServers(logger *slog.Logger, broken []config.SourceError) {
	for _, b := range broken {
		logger.Warn("skipping a server whose config fails to load", "server", b.ServerName, "err", b.Err)
	}
}

func loadOneServer(configDir, name string) (*config.Config, *config.ServerConfig, error) {
	cfg, err := config.LoadMain(configDir)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	sc, err := config.LoadServer(configDir, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, fmt.Errorf("server %q not found", name)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load %s: %w", name, err)
	}
	return cfg, &sc, nil
}
