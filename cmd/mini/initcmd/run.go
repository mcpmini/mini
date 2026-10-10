package initcmd

import (
	"context"
	"slices"
)

// Run is one init run, from its first save to its report. Checking and ChecksChanged are safe from
// any goroutine; the other methods belong to one.
type Run struct {
	Plan    Plan
	setup   Setup
	session *session
	last    syncResult
}

func (s Setup) Start(p Plan) *Run {
	return &Run{Plan: p, setup: s, session: s.newSession()}
}

// Save writes the plan's servers and starts checking the new ones for OAuth. It returns the servers
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

// Finish lets the checks end and connects the agents before reporting: the report reads both the
// servers and the agents' configs.
func (r *Run) Finish(ctx context.Context, connect ConnectParams) Report {
	r.session.WaitChecks()
	connected := r.setup.connectAgents(ctx, connect)
	report := r.report()
	report.Connected = connected
	return report
}

// Abandon cancels the checks and reports what the last save wrote, connecting no agent.
func (r *Run) Abandon() Report {
	r.session.Close()
	return r.report()
}

// Close cancels any check still running, so nothing is written after it returns.
func (r *Run) Close() {
	r.session.Close()
}

func (r *Run) report() Report {
	p := r.Plan
	report := r.setup.report()
	report.Import = p.Import
	adds := planAdds(p.Import.picked(), p.Add, p.written)
	report.AlreadyConfigured, report.AddCoveredByImport = adds.alreadyConfigured, adds.coveredByImport
	report.WriteErrors = r.last.Failed
	report.Import.keepOnly(report.Import.importedOf(r.session.Written()))
	report.Servers, report.ReadServersErr = ServerStatuses(r.setup.ConfigDir, p.Catalog)
	// A check that never finished leaves its server marked as maybe needing a login.
	markUnchecked(report.Servers, r.session.Unchecked())
	return report
}

func markUnchecked(statuses []ServerStatus, unchecked []string) {
	for i, status := range statuses {
		if status.Readiness == Ready && slices.Contains(unchecked, status.Name) {
			statuses[i].Readiness = MayNeedLogin
		}
	}
}
