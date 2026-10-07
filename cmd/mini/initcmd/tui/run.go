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

// Run shows the screens, writes the picks when the user moves past Catalog, and connects the agents
// the user picked on finishing.
func Run(p Params) (Outcome, error) {
	plan, err := p.Setup.Plan()
	if err != nil {
		return Outcome{}, err
	}
	r := &run{p: p, plan: plan, session: p.Setup.NewSession()}
	defer r.session.Close()
	r.ui = newScreens(p, &r.plan, r.session)
	a := newApp(r.ui.list())
	a.saves = savePoint{after: r.ui.savePoint(), save: r.save}
	if !a.hasScreens() {
		r.save()
		return r.outcome(false), nil
	}
	err = p.program()(a)
	// A login still waiting on the browser when the UI ends would otherwise save a token later.
	r.ui.logins.cancelLogin()
	if !a.saves.saved {
		return Outcome{Quit: a.quit || err != nil}, err
	}
	return r.outcome(a.quit || err != nil), err
}

type run struct {
	p       Params
	plan    initcmd.Plan
	session *initcmd.Session
	ui      screens
	last    initcmd.SyncResult
}

func (r *run) save() {
	r.ui.pick(&r.plan)
	r.last = r.session.Sync(r.plan.Servers())
}

func (r *run) outcome(leftEarly bool) Outcome {
	// No check may write while the report reads the servers.
	if leftEarly {
		r.session.Close()
	} else {
		r.session.WaitChecks()
	}
	r.plan.Catalog = r.ui.catalog(r.plan)
	var connected []initcmd.AgentResult
	// A ctrl+c queued behind the enter that chose to connect still ends the run early.
	if !leftEarly {
		connected = r.p.Setup.Connect(context.Background(), r.ui.connects.picked(), r.ui.connects.chosen)
	}
	// The report reads the agents' configs, so it follows the connect that changed them.
	report := r.p.Setup.Report(r.plan, r.session, r.last)
	report.Connected = connected
	return Outcome{Quit: leftEarly, Saved: true, Report: report}
}

type screens struct {
	imports  *importScreen
	catalogs *catalogScreen
	logins   *loginsScreen
	connects *connectScreen
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
	ui := screens{
		imports:  imports,
		catalogs: catalogs,
		connects: newConnectScreen(p.Setup.AgentsToConnect, p.Setup.AgentsWithMini()),
	}
	ui.logins = newLoginsScreen(loginsParams{
		statuses: func() ([]initcmd.ServerStatus, error) {
			return initcmd.ServerStatuses(p.Setup.ConfigDir, ui.catalog(*plan))
		},
		checking:   session.Running,
		changed:    session.Changed(),
		startLogin: p.StartLogin,
	})
	return ui
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
