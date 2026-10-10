package main

import (
	"context"
	"errors"
	"log"

	"github.com/mcpmini/mini/cmd/mini/initcmd/tui"
)

func startInitLogin(ctx context.Context, configDir, name string) (tui.Login, error) {
	cfg, sc, err := loadOAuthServerAndConfig(configDir, name, log.Writer())
	if err != nil {
		return tui.Login{}, err
	}
	login, err := startBrowserLogin(ctx, pkceFlowParamsFor(configDir, cfg, sc))
	if err != nil {
		return tui.Login{}, err
	}
	wait := func() error {
		_, err := login.wait()
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			// The screen shows the error's first line; a token endpoint's response body follows it.
			log.Printf("login to %s failed: %v", name, err)
		}
		return err
	}
	return tui.Login{URL: login.url, Wait: wait}, nil
}
