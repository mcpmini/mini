// Package tui is init's full-screen UI. It presents initcmd's plans; initcmd does the writing.
package tui

import (
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/mcpmini/mini/cmd/mini/initcmd"
	"github.com/mcpmini/mini/internal/catalog"
)

type Params struct {
	Setup initcmd.Setup
	// LoadCatalog may run on another goroutine while the UI is shown.
	LoadCatalog func() (catalog.Catalog, error)
	// StartLogin begins a server's browser login; it must not print, since the UI owns the terminal.
	StartLogin func(ctx context.Context, name string) (Login, error)
	// Program runs the UI; nil runs it in the terminal.
	Program func(m tea.Model) error
}

// Outcome is how the UI ended. Quit covers the program failing too, before or after the save.
type Outcome struct {
	Quit   bool
	Saved  bool
	Report initcmd.Report
}

// Run shows the screens, saves the picks on leaving Catalog, and connects the picked agents on finishing.
func Run(p Params) (Outcome, error) {
	plan, err := p.Setup.Plan()
	if err != nil {
		return Outcome{}, err
	}
	f := &flow{run: p.Setup.Start(plan)}
	defer f.run.Close()
	f.ui = newScreens(p, f.run)
	a := newApp(f.ui.list())
	a.saves = savePoint{after: f.ui.savePoint(), save: f.save}
	if !a.hasScreens() {
		f.save()
		return f.outcome(false), nil
	}
	err = p.program()(a)
	// A login still waiting on the browser when the UI ends would otherwise save a token later.
	f.ui.logins.cancelLogin()
	f.ui.connects.checks.cancelAndWait()
	if !a.saves.saved {
		return Outcome{Quit: a.quit || err != nil}, err
	}
	return f.outcome(a.quit || err != nil), err
}

type flow struct {
	run *initcmd.Run
	ui  screens
}

func (f *flow) save() {
	f.ui.pick(&f.run.Plan)
	f.ui.logins.forget(f.run.Save())
}

func (f *flow) outcome(leftEarly bool) Outcome {
	f.run.Plan.Catalog = f.ui.catalog(f.run.Plan)
	// A ctrl+c queued behind the enter that chose to connect still ends the run early.
	out := Outcome{Quit: leftEarly, Saved: true}
	if leftEarly {
		out.Report = f.run.Abandon()
	} else {
		out.Report = f.run.Finish(context.Background(), f.ui.connects.chosenConnect())
	}
	out.Report.Import.DropOfferedSkips()
	return out
}

type screens struct {
	imports  *importScreen
	catalogs *catalogScreen
	logins   *loginsScreen
	connects *connectScreen
}

func newScreens(p Params, run *initcmd.Run) screens {
	plan := &run.Plan
	imports := newImportScreen(plan.Import)
	catalogs := newCatalogScreen(
		catalogParams{load: p.LoadCatalog, inMini: plan.InMini, imports: imports.ticked},
	)
	if imports.empty() {
		// Nothing to look at while it loads, so wait: if the catalog is empty too, Catalog is skipped.
		catalogs.update(catalogs.start()())
	}
	ui := screens{
		imports:  imports,
		catalogs: catalogs,
		connects: newConnects(p.Setup),
	}
	ui.logins = newLoginsScreen(loginsParams{
		statuses: func() ([]initcmd.ServerStatus, error) {
			return initcmd.ServerStatuses(p.Setup.ConfigDir, ui.catalog(*plan))
		},
		checking:   run.Checking,
		changed:    run.ChecksChanged(),
		startLogin: p.StartLogin,
	})
	return ui
}

func newConnects(setup initcmd.Setup) *connectScreen {
	return newConnectScreen(connectParams{
		agents:   setup.AgentsToConnect,
		withMini: setup.AgentsWithMini(),
		plan:     func() (connectPlan, error) { return setup.PlanConnect() },
	})
}

func (ui screens) list() []screen {
	return []screen{ui.imports, ui.catalogs, ui.logins, ui.connects}
}

// The picks are written on leaving Catalog, so Logins works on configured servers.
func (ui screens) savePoint() int {
	return slices.Index(ui.list(), screen(ui.catalogs))
}

func (ui screens) pick(plan *initcmd.Plan) {
	ui.imports.pick(plan.Import.Candidates)
	plan.Add = ui.catalogs.picks()
	plan.Catalog = ui.catalog(*plan)
}

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
