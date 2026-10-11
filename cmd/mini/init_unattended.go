package main

import (
	"errors"
	"fmt"
	"os"

	"charm.land/lipgloss/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

var errInitIncomplete = errors.New("init didn't finish everything; see above")

// summaryStyle colors the summary only when a person is reading it at a terminal.
func summaryStyle() initcmd.SummaryStyle {
	if !isTerminal(os.Stdout) {
		return initcmd.SummaryStyle{}
	}
	warning := lipgloss.NewStyle().Bold(true)
	code := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	return initcmd.SummaryStyle{
		Warning: func(s string) string { return warning.Render(s) },
		Code:    func(s string) string { return code.Render(s) },
	}
}

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
	fmt.Print(initcmd.Summary(report, summaryStyle()))
	if report.Failed() {
		return &exitError{code: 1, err: errInitIncomplete}
	}
	return nil
}
