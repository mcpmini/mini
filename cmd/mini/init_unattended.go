package main

import (
	"errors"
	"fmt"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

var errInitIncomplete = errors.New("init didn't finish everything; see above")

func (f initFlags) unattended() bool {
	return f.importAll || f.from != "" || f.addGiven
}

func runUnattendedInit(configDir string, f initFlags) error {
	setup, err := setupFromFlags(configDir, f)
	if err != nil {
		return err
	}
	if err := createConfigDirs(configDir); err != nil {
		return fmt.Errorf("create config dirs: %w", err)
	}
	return printReport(initcmd.RunUnattended(setup))
}

func printReport(report initcmd.Report) error {
	fmt.Print(initcmd.Summary(report))
	if report.Failed() {
		return &exitError{code: 1, err: errInitIncomplete}
	}
	return nil
}
