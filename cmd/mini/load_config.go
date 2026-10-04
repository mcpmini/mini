package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/mcpmini/mini/internal/config"
)

func loadConfig(configDir string) (*config.Config, config.Servers, error) {
	cfg, err := config.LoadMain(configDir)
	if err != nil {
		return nil, config.Servers{}, fmt.Errorf("load config: %w", err)
	}
	servers, err := config.LoadServers(configDir)
	if err != nil {
		return nil, config.Servers{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, servers, nil
}

func loadOneServer(configDir, name string, warnings io.Writer) (*config.Config, *config.ServerConfig, error) {
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
	if sc.ProjectionsErr != nil {
		warnUnprojected(warnings, *sc.ProjectionsErr)
	}
	return cfg, &sc, nil
}

func warnServerProblems(out io.Writer, servers config.Servers) {
	for _, se := range servers.Broken {
		fmt.Fprintf(out, "warning: skipping server %s: %v\n", se.ServerName, se.Err)
	}
	for _, se := range servers.BrokenProjections() {
		warnUnprojected(out, se)
	}
}

func warnUnprojected(out io.Writer, se config.SourceError) {
	fmt.Fprintf(out, "warning: skipping the projections of server %s: %v\n", se.ServerName, se.Err)
}

const unknownTransport = "-"

func singleLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}
