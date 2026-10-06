// Package tui is init's full-screen UI. It presents initcmd's plans and writes nothing itself.
package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/catalog"
)

type Params struct {
	Setup   initcmd.Setup
	Catalog catalog.Catalog
	// Program runs the UI; nil runs it in the terminal.
	Program func(m tea.Model) error
}

// Run shows the screens and returns the plan with the user's picks; quit means the user left
// without finishing, so nothing should be written.
func Run(p Params) (plan initcmd.Plan, quit bool, err error) {
	plan, err = p.Setup.Plan()
	if err != nil {
		return initcmd.Plan{}, false, err
	}
	imports := newImportScreen(plan.Import.Candidates)
	catalogs := newCatalogScreen(p.Catalog, plan.Available(p.Catalog.Entries), imports.ticked)
	a := newApp([]screen{imports, catalogs})
	if len(a.screens) == 0 {
		return plan, false, nil
	}
	if err := p.program()(a); err != nil || a.quit {
		return initcmd.Plan{}, a.quit, err
	}
	imports.pick(plan.Import.Candidates)
	plan.Add = catalogs.picks()
	return plan, false, nil
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
