package main

import (
	"context"
	"io"

	"github.com/mcpmini/mini/cmd/mini/initcmd/tui"
)

// startInitLogin is mini auth's login with the same timeout, printing nothing: the UI owns the screen.
func startInitLogin(configDir string) func(ctx context.Context, name string) (tui.Login, error) {
	return func(ctx context.Context, name string) (tui.Login, error) {
		cfg, sc, err := loadOAuthServerAndConfig(configDir, name, io.Discard)
		if err != nil {
			return tui.Login{}, err
		}
		p := pkceFlowParamsFor(configDir, cfg, sc)
		ctx, cancel := context.WithTimeout(ctx, loginTimeout)
		login, err := beginBrowserLogin(ctx, p)
		if err != nil {
			cancel()
			return tui.Login{}, err
		}
		wait := func() error {
			defer cancel()
			_, err := finishBrowserLogin(ctx, login, p)
			return err
		}
		return tui.Login{URL: login.AuthURL(), Wait: wait}, nil
	}
}
