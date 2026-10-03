package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"

	"github.com/mcpmini/mini/internal/config"
)

func loadConfig(configDir string) (*config.Config, config.Servers, error) {
	cfg, err := config.LoadMain(configDir)
	if err != nil {
		return nil, config.Servers{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, config.LoadServers(configDir), nil
}

// loadOneServer doesn't load the other server files, so one that is broken can't block this one.
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
		return nil, nil, err
	}
	return cfg, &sc, nil
}

func warnBrokenServers(out io.Writer, broken []config.SourceError) {
	for _, b := range broken {
		fmt.Fprintf(out, "warning: skipping server %s: %v\n", b.ServerName, b.Err)
	}
}

func logBrokenServers(logger *slog.Logger, broken []config.SourceError) {
	for _, b := range broken {
		logger.Warn("skipping server whose config fails to load", "server", b.ServerName, "err", b.Err)
	}
}
