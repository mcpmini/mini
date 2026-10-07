package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mcpmini/mini/cmd/mini/initcmd/tui"
	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/clock"
)

// startInitLogin is mini auth's browser login without its output: the UI owns the screen.
func startInitLogin(configDir string) func(ctx context.Context, name string) (tui.Login, error) {
	return func(ctx context.Context, name string) (tui.Login, error) {
		cfg, sc, err := loadOneServer(configDir, name, io.Discard)
		if err != nil {
			return tui.Login{}, err
		}
		if err := auth.ValidateOAuthServer(name, *sc); err != nil {
			return tui.Login{}, err
		}
		params := auth.BeginLoginParams{ConfigDir: configDir, ServerName: name, Clock: clock.System()}
		login, err := auth.BeginLogin(ctx, sc, params)
		if err != nil {
			return tui.Login{}, err
		}
		// The screen shows the URL, so a browser that doesn't open only costs a click.
		authOpener(cfg, *sc)(login.AuthURL()) //nolint:errcheck
		wait := func() error { return waitAndSave(ctx, login, savedAs{configDir, name}) }
		return tui.Login{URL: login.AuthURL(), Wait: wait}, nil
	}
}

type savedAs struct {
	configDir string
	name      string
}

func waitAndSave(ctx context.Context, login *auth.BrowserLogin, to savedAs) error {
	defer login.Close() //nolint:errcheck // Close always returns nil
	token, err := login.Wait(ctx)
	if err != nil {
		return err
	}
	if err := auth.Save(to.configDir, to.name, token); err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	return nil
}
