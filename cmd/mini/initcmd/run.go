package initcmd

import (
	"context"
	"log"
	"slices"

	"github.com/mcpmini/mini/internal/config"
)

// Run is one init run, from its first save to its report. It saves to a stage, and only Finish
// commits it to mini. Checking and ChecksChanged are safe from any goroutine; the other methods
// belong to one.
type Run struct {
	Plan    Plan
	setup   Setup
	stage   *stage
	session *session
	last    syncResult
	commit  []ServerError
}

func (s Setup) Start(p Plan) (*Run, error) {
	st, err := newStage(s.ConfigDir)
	if err != nil {
		return nil, err
	}
	return &Run{Plan: p, setup: s, stage: st, session: s.newSession(st.dir)}, nil
}

// StageDir is where the run saves until Finish commits it; a login saved there is committed too.
func (r *Run) StageDir() string {
	return r.stage.dir
}

// Save stages the plan's servers and starts checking the new ones for OAuth. It returns the servers
// it removed, credentials included, so a login done for them no longer applies.
func (r *Run) Save() (removed []string) {
	r.last = r.session.Sync(r.Plan.Servers())
	return r.last.Removed
}

// Added names the servers this run added to mini, less any unticked since.
func (r *Run) Added() []string {
	return r.session.Written()
}

// Checking names the servers whose OAuth check is still running.
func (r *Run) Checking() map[string]bool {
	return r.session.Running()
}

// ChecksChanged receives after a check starts or finishes; a receive may cover several.
func (r *Run) ChecksChanged() <-chan struct{} {
	return r.session.Changed()
}

// Finish lets the checks end, commits the stage, and connects the agents before reporting: the
// report reads both the servers and the agents' configs.
func (r *Run) Finish(ctx context.Context, connect ConnectParams) Report {
	r.session.WaitChecks()
	r.commit = r.stage.commit()
	r.discardStage()
	connected := r.setup.connectAgents(ctx, connect)
	report := r.report()
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

func (r *Run) report() Report {
	p := r.Plan
	report := r.setup.report()
	report.Import = p.Import
	adds := planAdds(p.Import.picked(), p.Add, p.written)
	report.AlreadyConfigured, report.AddCoveredByImport = adds.alreadyConfigured, adds.coveredByImport
	report.WriteErrors = slices.Concat(r.last.Failed, r.commit)
	report.Import.keepOnly(report.Import.importedOf(r.committed()))
	report.Servers, report.ReadServersErr = ServerStatuses(r.setup.ConfigDir, p.Catalog)
	return report
}

func (r *Run) committed() []string {
	return slices.DeleteFunc(r.session.Written(), func(name string) bool {
		return !config.ServerFileExists(r.setup.ConfigDir, name)
	})
}
