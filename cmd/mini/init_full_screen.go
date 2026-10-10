package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/mcpmini/mini/cmd/mini/initcmd/tui"
)

var errInitQuit = errors.New("init quit; nothing was written")

func runFullScreenInit(configDir string) error {
	setup, err := setupFromFlags(configDir, initFlags{importAll: true})
	if err != nil {
		return err
	}
	logs := newUILogs(configDir)
	restore := logs.redirect()
	out, runErr := tui.Run(tui.Params{
		Setup:       setup,
		LoadCatalog: publishedCatalogSource().load,
		StartLogin:  startInitLogin,
	})
	restore()
	err = reportFullScreenInit(configDir, out, runErr)
	if path, ok := logs.written(); ok {
		fmt.Fprintf(os.Stderr, "mini: messages logged during setup are in %s\n", path)
	}
	return err
}

func reportFullScreenInit(configDir string, out tui.Outcome, runErr error) error {
	if !out.Saved {
		if runErr != nil {
			return runErr
		}
		return &exitError{code: 1, err: errInitQuit}
	}
	reportErr := printReport(out.Report)
	if err := createConfigDirs(configDir); err != nil {
		return fmt.Errorf("create config dirs: %w", err)
	}
	return reportErr
}
