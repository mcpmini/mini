package initcmd

import (
	"context"
	"log"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
)

// Run is one init run, from its first save to its report. The servers it adds wait in a stage until
// Finish commits them to mini. Checking and ChecksChanged are safe from any goroutine; the other
// methods belong to one.
type Run struct {
	Plan    Plan
	setup   Setup
	stage   *stage
	session *session
	last    syncResult
}

func (s Setup) Start(p Plan) *Run {
	st := newStage(s.ConfigDir)
	return &Run{Plan: p, setup: s, stage: st, session: s.newSession(st.dir)}
}

// Save stages the plan's servers and starts checking the new ones for OAuth. It returns the servers
// it removed, credentials included, so a login done for them no longer applies.
func (r *Run) Save() (removed []string) {
	if err := r.stage.create(); err != nil {
		r.last = syncResult{Failed: failedAll(r.Plan.Servers(), err)}
		return nil
	}
	r.stage.touch()
	r.last = r.session.Sync(r.Plan.Servers())
	return r.last.Removed
}

func failedAll(servers []config.ServerConfig, err error) []ServerError {
	var failed []ServerError
	for _, sc := range servers {
		failed = append(failed, ServerError{Name: sc.Name, Err: err})
	}
	return failed
}

// Added names the servers this run adds to mini, less any unticked since.
func (r *Run) Added() []string {
	return r.session.Written()
}

// ConfigDirFor is where a server's login is saved: the stage for one this run adds, mini for the rest.
func (r *Run) ConfigDirFor(server string) string {
	if slices.Contains(r.session.Written(), server) {
		return r.stage.dir
	}
	return r.setup.ConfigDir
}

// ServerStatuses are mini's servers with the ones this run adds.
func (r *Run) ServerStatuses(entries []catalog.Entry) ([]ServerStatus, error) {
	inMini, err := ServerStatuses(r.setup.ConfigDir, entries)
	if err != nil || !r.stage.created {
		return inMini, err
	}
	added, err := ServerStatuses(r.stage.dir, entries)
	all := slices.Concat(inMini, added)
	slices.SortFunc(all, func(a, b ServerStatus) int { return strings.Compare(a.Name, b.Name) })
	return all, err
}

// Checking names the servers whose OAuth check is still running.
func (r *Run) Checking() map[string]bool {
	return r.session.Running()
}

// ChecksChanged receives after a check starts or finishes; a receive may cover several.
func (r *Run) ChecksChanged() <-chan struct{} {
	return r.session.Changed()
}

// Finish lets the checks end, commits the staged servers, and connects the agents before
// reporting: the report reads both the servers and the agents' configs.
func (r *Run) Finish(ctx context.Context, connect ConnectParams) Report {
	r.session.WaitChecks()
	committed := r.stage.commit(r.session.Written())
	r.discardStage()
	connected := r.setup.connectAgents(ctx, connect)
	report := r.report(committed)
	report.Connected = connected
	return report
}

// Close cancels any check still running and discards what Finish didn't commit.
func (r *Run) Close() {
	r.session.Close()
	r.discardStage()
}

func (r *Run) discardStage() {
	if err := r.stage.discard(); err != nil {
		log.Printf("init: remove %s: %v", r.stage.dir, err)
	}
}

func (r *Run) report(c commitResult) Report {
	p := r.Plan
	report := r.setup.report()
	report.Import = p.Import
	adds := planAdds(p.Import.picked(), p.Add, p.written)
	report.AlreadyConfigured, report.AddCoveredByImport = adds.alreadyConfigured, adds.coveredByImport
	report.WriteErrors = slices.Concat(r.last.Failed, c.failed)
	report.Import.keepOnly(report.Import.importedOf(c.committed))
	report.Servers, report.ReadServersErr = ServerStatuses(r.setup.ConfigDir, p.Catalog)
	return report
}
