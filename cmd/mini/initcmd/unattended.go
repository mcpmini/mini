package initcmd

import (
	"maps"
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
	// Import is the plan, with notes only for the imports written and skip lines only for entries left behind.
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

// Available is the catalog servers mini doesn't have yet.
func (p Plan) Available(entries []catalog.Entry) []catalog.Entry {
	return AvailableEntries(entries, p.written)
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

// Plan is an import plan, the catalog servers to add, and the servers mini had when it was made.
// The UI changes which candidates are picked and what is added before the plan is written.
type Plan struct {
	Import  ImportPlan
	Add     []catalog.Entry
	Catalog []catalog.Entry
	written WrittenServers
}

func (s Setup) Plan() (Plan, error) {
	written, err := readWrittenServers(s.ConfigDir)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Import:  PlanImport(ImportParams{Agents: s.Import, Written: written, SelfPath: s.SelfPath}),
		Add:     s.Add,
		Catalog: s.Catalog,
		written: written,
	}, nil
}

func (s Setup) Write(p Plan) Report {
	report := s.report()
	report.Import = p.Import
	adds := planAdds(p.Import.picked(), p.Add, p.written)
	report.AlreadyConfigured, report.AddCoveredByImport = adds.alreadyConfigured, adds.coveredByImport
	added, writeErrors := addServers(s.ConfigDir, adds.write)
	report.WriteErrors = writeErrors
	CheckOAuth(s.ConfigDir, OAuthTargets(s.ConfigDir, added), clock.System())
	report.Import.keepOnly(report.Import.importedOf(added))
	report.Servers, report.ReadServersErr = ServerStatuses(s.ConfigDir, p.Catalog)
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

// A catalog add can share an unpicked candidate's name, so a written name alone doesn't mean an import.
func (plan ImportPlan) importedOf(added []string) []Candidate {
	var imported []Candidate
	for _, c := range plan.Candidates {
		if c.Picked && slices.Contains(added, c.Server.Name) {
			imported = append(imported, c)
		}
	}
	return imported
}

// The plan's notes and skips then say what this run did: a picked candidate that failed to write
// has no notes, and the entries of a candidate the user picked aren't listed as left behind.
func (plan *ImportPlan) keepOnly(imported []Candidate) {
	notImported := func(name string) bool {
		return !slices.ContainsFunc(imported, func(c Candidate) bool { return c.Server.Name == name })
	}
	maps.DeleteFunc(plan.DroppedSettings, func(name string, _ []string) bool { return notImported(name) })
	maps.DeleteFunc(plan.StaticHeaders, func(name string, _ map[string]string) bool { return notImported(name) })
	plan.Skipped = slices.DeleteFunc(plan.Skipped, func(s SkippedServer) bool {
		entry := AgentEntry{Agent: s.Agent, Name: s.Name}
		imports := slices.ContainsFunc(imported, func(c Candidate) bool { return slices.Contains(c.From, entry) })
		// A second config's line says the first is imported instead; untrue once the user unticked it.
		return imports || (s.Reason == SkipSecondConfig && !importsTheName(imported, s.Name))
	})
}

func importsTheName(imported []Candidate, name string) bool {
	return slices.ContainsFunc(imported, func(c Candidate) bool {
		return slices.ContainsFunc(
			c.From,
			func(e AgentEntry) bool { return NormalizeName(e.Name) == NormalizeName(name) },
		)
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
