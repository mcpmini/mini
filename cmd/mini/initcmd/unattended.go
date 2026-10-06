package initcmd

import (
	"slices"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

// Setup is what one init run imports and adds. Writing it never edits an agent or opens a browser.
type Setup struct {
	ConfigDir string
	Import    []agents.Agent
	Add       []catalog.Entry
	// Catalog tells which servers need a token or the user's own app.
	Catalog         []catalog.Entry
	AgentsToConnect []agents.Agent
	SelfPath        string
}

// Report is what a run did, for the summary.
type Report struct {
	ConfigDir string
	// Import is the plan, less the notes of servers that failed to write.
	Import             ImportPlan
	AlreadyConfigured  []string
	AddCoveredByImport []string
	WriteErrors        []ServerError
	Servers            []ServerStatus
	Agents             AgentConnections
	ReadServersErr     error
}

func (r Report) Failed() bool {
	return len(r.WriteErrors) > 0 || len(r.Import.Unreadable) > 0 || r.ReadServersErr != nil
}

func RunUnattended(s Setup) Report {
	plan, err := s.Plan()
	if err != nil {
		report := s.report()
		report.ReadServersErr = err
		return report
	}
	return s.Write(plan)
}

// Plan is an import plan and the servers mini had when it was made. The Import screen changes
// which candidates are picked before the plan is written.
type Plan struct {
	Import  ImportPlan
	written WrittenServers
}

func (s Setup) Plan() (Plan, error) {
	written, err := readWrittenServers(s.ConfigDir)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Import:  PlanImport(ImportParams{Agents: s.Import, Written: written, SelfPath: s.SelfPath}),
		written: written,
	}, nil
}

func (s Setup) Write(p Plan) Report {
	report := s.report()
	report.Import = p.Import
	adds := planAdds(p.Import.picked(), s.Add, p.written)
	report.AlreadyConfigured, report.AddCoveredByImport = adds.alreadyConfigured, adds.coveredByImport
	added, writeErrors := addServers(s.ConfigDir, adds.write)
	report.WriteErrors = writeErrors
	CheckOAuth(s.ConfigDir, OAuthTargets(s.ConfigDir, added), clock.System())
	report.Import.keepOnly(added)
	report.Servers, report.ReadServersErr = ServerStatuses(s.ConfigDir, s.Catalog)
	return report
}

func (s Setup) report() Report {
	return Report{ConfigDir: s.ConfigDir, Agents: ClassifyAgents(s.ConfigDir, s.SelfPath, s.AgentsToConnect)}
}

type ServerError struct {
	Name string
	Err  error
}

func addServers(configDir string, servers []config.ServerConfig) ([]string, []ServerError) {
	var written []string
	var failed []ServerError
	for _, sc := range servers {
		if _, err := ops.AddServer(configDir, sc); err != nil {
			failed = append(failed, ServerError{Name: sc.Name, Err: err})
			continue
		}
		written = append(written, sc.Name)
	}
	return written, failed
}

func (plan ImportPlan) picked() []config.ServerConfig {
	var servers []config.ServerConfig
	for _, c := range plan.Candidates {
		if c.Picked {
			servers = append(servers, c.Server)
		}
	}
	return servers
}

// The plan's notes and skips then say what this run did: a picked candidate that failed to write
// has no notes, and the entries of a candidate the user picked aren't listed as left behind.
func (plan *ImportPlan) keepOnly(written []string) {
	for name := range plan.DroppedSettings {
		if !slices.Contains(written, name) {
			delete(plan.DroppedSettings, name)
		}
	}
	for name := range plan.StaticHeaders {
		if !slices.Contains(written, name) {
			delete(plan.StaticHeaders, name)
		}
	}
	plan.Skipped = slices.DeleteFunc(plan.Skipped, func(s SkippedServer) bool {
		return plan.writtenFrom(written, AgentEntry{Agent: s.Agent, Name: s.Name})
	})
}

func (plan ImportPlan) writtenFrom(written []string, entry AgentEntry) bool {
	return slices.ContainsFunc(plan.Candidates, func(c Candidate) bool {
		return slices.Contains(written, c.Server.Name) && slices.Contains(c.From, entry)
	})
}

type addPlan struct {
	write             []config.ServerConfig
	alreadyConfigured []string
	coveredByImport   []string
}

func planAdds(imports []config.ServerConfig, add []catalog.Entry, written WrittenServers) addPlan {
	p := addPlan{write: slices.Clone(imports)}
	configuredKeys := NewConfiguredKeys(written)
	imported := NewConfiguredKeys(p.write)
	for _, entry := range add {
		switch {
		case configuredKeys.Has(entry):
			p.alreadyConfigured = append(p.alreadyConfigured, entry.Name)
		case imported.Has(entry):
			p.coveredByImport = append(p.coveredByImport, entry.Name)
		default:
			p.write = append(p.write, CatalogServer(entry))
		}
	}
	return p
}

// CatalogServer is the server config written for a catalog entry.
func CatalogServer(entry catalog.Entry) config.ServerConfig {
	sc := config.ServerConfig{Name: entry.Name, Transport: "http", URL: entry.URL}
	if entry.Auth == catalog.AuthOAuth2 && !sc.HasBundledAuth() {
		sc.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2}
	}
	return sc
}
