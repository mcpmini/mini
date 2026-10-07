// Package tui is init's full-screen UI. It presents initcmd's plans; initcmd does the writing.
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

// Outcome is how the UI ended. Saved means the picks were written, and Report says what that did;
// Quit means the user left early, after the save or before it.
type Outcome struct {
	Quit   bool
	Saved  bool
	Report initcmd.Report
}

// Run shows the screens and writes the picks when the user moves past Catalog.
func Run(p Params) (Outcome, error) {
	plan, err := p.Setup.Plan()
	if err != nil {
		return Outcome{}, err
	}
	session := p.Setup.NewSession()
	defer session.Close()
	ui := newScreens(p, &plan, session)
	var synced initcmd.Synced
	save := func() {
		ui.pick(&plan)
		result := session.Sync(plan.Servers())
		synced = initcmd.Synced{Written: session.Written(), Failed: result.Failed}
	}
	a := newApp(ui.list())
	a.saves = savePoint{after: 1, save: save}
	if !a.hasScreens() {
		save()
	} else if err := p.program()(a); err != nil {
		return Outcome{Saved: a.saves.saved}, err
	}
	if a.quit && !a.saves.saved {
		return Outcome{Quit: true}, nil
	}
	if !a.quit {
		session.WaitChecks()
	}
	return Outcome{Quit: a.quit, Saved: true, Report: p.Setup.Report(plan, synced)}, nil
}

type screens struct {
	imports  *importScreen
	catalogs *catalogScreen
	logins   *loginsScreen
}

func newScreens(p Params, plan *initcmd.Plan, session *initcmd.Session) screens {
	imports := newImportScreen(plan.Import.Candidates)
	catalogs := newCatalogScreen(
		catalogParams{load: p.LoadCatalog, offered: plan.Available, imports: imports.ticked},
	)
	if imports.empty() {
		// Nothing to look at while it loads, so wait: if the catalog is empty too, Catalog is skipped.
		catalogs.update(catalogs.start()())
	}
	ui := screens{imports: imports, catalogs: catalogs}
	ui.logins = newLoginsScreen(loginsParams{
		statuses: func() ([]initcmd.ServerStatus, error) {
			return initcmd.ServerStatuses(p.Setup.ConfigDir, ui.catalog(*plan))
		},
		checking: session.Checking,
		changed:  session.Changed(),
	})
	return ui
}

func (ui screens) list() []screen {
	return []screen{ui.imports, ui.catalogs, ui.logins}
}

func (ui screens) pick(plan *initcmd.Plan) {
	ui.imports.pick(plan.Import.Candidates)
	plan.Add = ui.catalogs.picks()
	plan.Catalog = ui.catalog(*plan)
}

// The catalog the user picked from, once it loaded; until then, the one the plan started with.
func (ui screens) catalog(plan initcmd.Plan) []catalog.Entry {
	if entries, ok := ui.catalogs.entries(); ok {
		return entries
	}
	return plan.Catalog
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
