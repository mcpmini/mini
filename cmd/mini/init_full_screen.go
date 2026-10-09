package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/mcpmini/mini/cmd/mini/initcmd/tui"
)

var (
	errInitQuit          = errors.New("init quit; nothing was written")
	errInitQuitAfterSave = errors.New("init quit; the servers above were saved and no agent was changed")
)

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
		StartLogin:  startInitLogin(configDir),
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
	// The servers are already written, so the summary prints however the UI ended.
	reportErr := printReport(out.Report)
	if err := createConfigDirs(configDir); err != nil {
		return fmt.Errorf("create config dirs: %w", err)
	}
	switch {
	case runErr != nil:
		return runErr
	case out.Quit:
		return &exitError{code: 1, err: errInitQuitAfterSave}
	}
	return reportErr
}
