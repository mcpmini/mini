package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

const addConnectTimeout = 15 * time.Second

// connectAndAuthorizeIfNeeded probes a freshly-added HTTP server and, if it turns out to need
// OAuth (either just-discovered via a 401, or already known via a bundled default like
// atlassian/linear/slack), immediately runs the same interactive authorization flow `mini auth`
// uses — so `mini add` leaves the server ready to use in one step.
func connectAndAuthorizeIfNeeded(configDir, name string, out io.Writer) {
	sc, ok := loadServerConfigForAdd(configDir, name)
	if !ok || !sc.IsHTTPTransport() {
		return
	}
	if sc.Auth == nil {
		probeAndReport(configDir, sc, out)
		sc, ok = loadServerConfigForAdd(configDir, name)
		if !ok {
			return
		}
	}
	if sc.Auth == nil || sc.Auth.Type != "oauth2" {
		return
	}
	authorizeServer(configDir, name, sc, out)
}

func loadServerConfigForAdd(configDir, name string) (config.ServerConfig, bool) {
	_, servers, err := config.Load(configDir)
	if err != nil {
		return config.ServerConfig{}, false
	}
	sc := config.FindServer(servers, name)
	if sc == nil {
		return config.ServerConfig{}, false
	}
	return *sc, true
}

func probeAndReport(configDir string, sc config.ServerConfig, out io.Writer) {
	if err := probeConnection(configDir, sc); err != nil {
		fmt.Fprintf(out, "note: could not connect to %s yet; run `mini test` to retry\n", sc.Name)
		return
	}
	fmt.Fprintf(out, "connected to %s\n", sc.Name)
}

func probeConnection(configDir string, sc config.ServerConfig) error {
	cfg, _, err := config.Load(configDir)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.NewWithConfigDir(cfg, configDir, logger)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), addConnectTimeout)
	defer cancel()
	return srv.AddUpstream(ctx, sc)
}

func authorizeServer(configDir, name string, sc config.ServerConfig, out io.Writer) {
	cfg, _, err := config.Load(configDir)
	if err != nil {
		fmt.Fprintf(out, "warning: reload config for auth: %v\n", err)
		return
	}
	fmt.Fprintf(out, "%s requires OAuth authorization\n", name)
	runPKCEFlow(pkceFlowParams{
		configDir:  configDir,
		serverName: name,
		opener:     authOpener(sc.Auth.BrowserCmd, cfg.BrowserCommand, cfg.DisableAuthBrowserOpen),
		sc:         &sc,
	})
}
