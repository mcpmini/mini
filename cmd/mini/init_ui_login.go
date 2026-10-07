package main

import (
	"context"
	"log"

	"github.com/mcpmini/mini/cmd/mini/initcmd/tui"
)

// startInitLogin is mini auth's login with the same timeout. While the UI owns the screen, log goes to init.log.
func startInitLogin(configDir string) func(ctx context.Context, name string) (tui.Login, error) {
	return func(ctx context.Context, name string) (tui.Login, error) {
		cfg, sc, err := loadOAuthServerAndConfig(configDir, name, log.Writer())
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
			if err != nil {
				// The screen shows the error's first line; a token endpoint's response body follows it.
				log.Printf("login to %s failed: %v", name, err)
			}
			return err
		}
		return tui.Login{URL: login.AuthURL(), Wait: wait}, nil
	}
}
