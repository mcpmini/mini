// Package tui is init's full-screen UI. It presents initcmd's plans and writes nothing itself.
package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/catalog"
)

type Params struct {
	Setup initcmd.Setup
	// LoadCatalog runs in the background while the Import screen is shown.
	LoadCatalog func() (catalog.Catalog, error)
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
	catalogs := newCatalogScreen(
		catalogParams{load: p.LoadCatalog, offered: plan.Available, imports: imports.ticked},
	)
	if imports.empty() {
		// Nothing to look at while it loads, so wait: if the catalog is empty too, no UI is shown.
		catalogs.update(catalogs.start()())
	}
	a := newApp([]screen{imports, catalogs})
	if !a.hasScreens() {
		return plan, false, nil
	}
	if err := p.program()(a); err != nil || a.quit {
		return initcmd.Plan{}, a.quit, err
	}
	imports.pick(plan.Import.Candidates)
	plan.Add = catalogs.picks()
	if entries, ok := catalogs.entries(); ok {
		plan.Catalog = entries
	}
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
