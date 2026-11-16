package main

import (
	"context"
	"errors"
	"log"
	"path/filepath"

	"github.com/mcpmini/mini/cmd/mini/initcmd/tui"
)

type initLoginStart func(ctx context.Context, configDir, name string) (tui.Login, error)

// initLoginStarter logs in through the daemon only for servers already in mini: a server this run
// adds saves its login to the stage, which the daemon must not see before init saves.
func initLoginStarter(rootConfigDir string) initLoginStart {
	return func(ctx context.Context, configDir, name string) (tui.Login, error) {
		cfg, sc, err := loadOAuthServerAndConfig(configDir, name, log.Writer())
		if err != nil {
			return tui.Login{}, err
		}
		params := pkceFlowParamsFor(configDir, cfg, sc)
		if filepath.Clean(configDir) == filepath.Clean(rootConfigDir) {
			params.reachDaemon = daemonReacher(configDir)
		}
		login, err := startBrowserLogin(ctx, params)
		if err != nil {
			return tui.Login{}, err
		}
		return tui.Login{URL: login.url, Wait: func() error { return waitForInitLogin(name, login) }}, nil
	}
}

// waitForInitLogin logs instead of printing, since init's screen owns the terminal.
func waitForInitLogin(name string, login browserLogin) error {
	result, err := login.wait()
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		// The screen shows the error's first line; a token endpoint's response body follows it.
		log.Printf("login to %s failed: %v", name, err)
	}
	if err == nil && result.connectErr != "" {
		log.Printf("authorized %s, but connecting to it failed: %s", name, result.connectErr)
	}
	return err
}
