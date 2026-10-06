package initcmd

import (
	"slices"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

// Unattended is init without the UI: it writes servers and never edits an agent or opens a browser.
type Unattended struct {
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

func RunUnattended(p Unattended) Report {
	report := Report{ConfigDir: p.ConfigDir, Agents: ClassifyAgents(p.ConfigDir, p.SelfPath, p.AgentsToConnect)}
	written, err := readWrittenServers(p.ConfigDir)
	if err != nil {
		report.ReadServersErr = err
		return report
	}
	report.Import = PlanImport(ImportParams{Agents: p.Import, Written: written, SelfPath: p.SelfPath})
	adds := planAdds(report.Import.Servers, p.Add, written)
	report.AlreadyConfigured, report.AddCoveredByImport = adds.alreadyConfigured, adds.coveredByImport
	added, writeErrors := addServers(p.ConfigDir, adds.write)
	report.WriteErrors = writeErrors
	CheckOAuth(p.ConfigDir, OAuthTargets(p.ConfigDir, added), clock.System())
	report.Import.dropNotesOf(writeErrors)
	report.Servers, report.ReadServersErr = ServerStatuses(p.ConfigDir, p.Catalog)
	return report
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

func (plan *ImportPlan) dropNotesOf(failures []ServerError) {
	for _, failure := range failures {
		delete(plan.DroppedSettings, failure.Name)
		delete(plan.StaticHeaders, failure.Name)
	}
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
