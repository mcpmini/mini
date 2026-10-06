// Package tui is init's full-screen UI. It presents initcmd's plans and writes nothing itself.
package tui

import (
	"io"
	"log"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
)

type Params struct {
	Setup initcmd.Setup
	// Program runs the UI; tests swap it for one that drives the model directly.
	Program func(m tea.Model) error
}

// Outcome is how the UI ended. Quit means the user left before anything was written.
type Outcome struct {
	Report initcmd.Report
	Quit   bool
	// LogFile is where log output went while the UI ran; empty when nothing was logged.
	LogFile string
}

func Run(p Params) (Outcome, error) {
	plan, err := p.Setup.Plan()
	if err != nil {
		return Outcome{}, err
	}
	imports := newImportScreen(plan.Import.Candidates)
	a := newApp([]screen{imports})
	var out Outcome
	if len(a.screens) > 0 {
		out.LogFile, err = runQuietly(p, a)
		if err != nil || a.quit {
			out.Quit = a.quit
			return out, err
		}
	}
	imports.pick(plan.Import.Candidates)
	out.Report = p.Setup.Write(plan)
	return out, nil
}

// Anything written to the terminal while the UI draws would corrupt it, so log output goes to a
// file for the UI's lifetime.
func runQuietly(p Params, a *app) (string, error) {
	path := filepath.Join(p.Setup.ConfigDir, "internal", "init.log")
	restore, err := logTo(path)
	if err != nil {
		return "", err
	}
	runErr := p.program()(a)
	logged := restore()
	if !logged {
		path = ""
	}
	return path, runErr
}

func (p Params) program() func(m tea.Model) error {
	if p.Program != nil {
		return p.Program
	}
	return func(m tea.Model) error {
		_, err := tea.NewProgram(m).Run()
		return err
	}
}

// logTo sends log output to path until the returned func restores it; that func reports whether
// anything was logged.
func logTo(path string) (restore func() bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	start, _ := f.Seek(0, io.SeekEnd) //nolint:errcheck // a failed seek reads as nothing logged before
	previous := log.Writer()
	log.SetOutput(f)
	return func() bool {
		log.SetOutput(previous)
		end, _ := f.Seek(0, io.SeekEnd) //nolint:errcheck // a failed seek reads as nothing logged
		_ = f.Close()                   //nolint:errcheck // the file was only appended to; log lines are best effort
		return end > start
	}, nil
}
