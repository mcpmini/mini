package main

import (
	"io"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/clock"
)

type oauthDetectParams struct {
	configDir string
	names     []string
	clock     clock.Clock
	errOut    io.Writer
}

func detectImportedOAuth(p oauthDetectParams) {
	targets := initcmd.OAuthTargets(p.configDir, p.names)
	if len(targets) == 0 {
		return
	}
	printNotice(p.errOut, "checking %d imported server(s) for OAuth...\n", len(targets))
	initcmd.CheckOAuth(p.configDir, targets, p.clock)
}
