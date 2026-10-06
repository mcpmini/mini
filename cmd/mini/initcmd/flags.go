package initcmd

import (
	"slices"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

// FlagRun is init without the UI: it writes servers and never edits an agent or opens a browser.
type FlagRun struct {
	ConfigDir string
	// Import holds the agents to import from: every detected one for --import, the one source
	// for --from, none for --add alone.
	Import []agents.Agent
	Add    []catalog.Entry
	// Catalog tells which servers need a token or the user's own app.
	Catalog []catalog.Entry
	// Connectable are the agents the summary shows how to connect by hand.
	Connectable []agents.Agent
	SelfPath    string
}

// Report is what a run did, for the summary.
type Report struct {
	ConfigDir   string
	Servers     []ServerStatus
	WriteErrors []ServerError
	Skipped     []SkippedServer
	// Ignored holds, per imported server, the agent settings mini doesn't carry over.
	Ignored map[string][]string
	// UnusedEnvHeaders holds, per imported server, each static header kept over the variable the
	// agent would read it from once that is set.
	UnusedEnvHeaders  map[string]map[string]string
	AlreadyConfigured []string
	// FromImport are --add names an imported server already covers, so the catalog's isn't added.
	FromImport []string
	// Unconnected are agents the summary shows how to connect by hand.
	Unconnected []agents.Agent
	// HasMini are agents whose mini entry serves this config directory; it is the user's, so
	// they get no step.
	HasMini []agents.Agent
	// InactiveMini are agents whose mini entry may not run these servers; it is the user's, so
	// the summary says what it should run instead of adding a second one.
	InactiveMini []agents.Agent
	Mini         agents.MiniEntry
	StatusErr    error
}

func (r Report) Failed() bool {
	return len(r.WriteErrors) > 0 || r.StatusErr != nil
}

func RunFlags(p FlagRun) Report {
	report := Report{ConfigDir: p.ConfigDir, Mini: MiniCommand(p.ConfigDir)}
	p.sortByMini(&report)
	configured, err := writtenServers(p.ConfigDir)
	if err != nil {
		report.StatusErr = err
		return report
	}
	plan := PlanImport(ImportParams{Agents: p.Import, Configured: configured, SelfPath: p.SelfPath})
	report.Skipped, report.Ignored, report.UnusedEnvHeaders = plan.Skipped, plan.Ignored, plan.UnusedEnvHeaders
	picks := flagPicks(plan.Servers, p.Add, configured)
	report.AlreadyConfigured, report.FromImport = picks.already, picks.fromImport
	written, writeErrors := addServers(p.ConfigDir, picks.want)
	report.WriteErrors = writeErrors
	CheckOAuth(p.ConfigDir, OAuthTargets(p.ConfigDir, written), clock.System())
	report.dropNotesOfFailedServers()
	report.Servers, report.StatusErr = ServerStatuses(p.ConfigDir, p.Catalog)
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

func (r *Report) dropNotesOfFailedServers() {
	for _, failure := range r.WriteErrors {
		delete(r.Ignored, failure.Name)
		delete(r.UnusedEnvHeaders, failure.Name)
	}
}

type picks struct {
	want       []config.ServerConfig
	already    []string
	fromImport []string
}

// An imported server wins over a catalog server it matches.
func flagPicks(imports []config.ServerConfig, add []catalog.Entry, configured []config.ServerConfig) picks {
	p := picks{want: slices.Clone(imports)}
	configuredKeys := NewConfiguredKeys(configured)
	imported := NewConfiguredKeys(p.want)
	for _, entry := range add {
		switch {
		case configuredKeys.Has(entry):
			p.already = append(p.already, entry.Name)
		case imported.Has(entry):
			p.fromImport = append(p.fromImport, entry.Name)
		default:
			p.want = append(p.want, CatalogServer(entry))
		}
	}
	return p
}

func (p FlagRun) sortByMini(r *Report) {
	existing := ApplyParams{ConfigDir: p.ConfigDir, SelfPath: p.SelfPath}
	for _, agent := range p.Connectable {
		entries, err := agent.Read(agent.ConfigPath)
		mini := NoMiniEntry
		if err == nil {
			mini = existing.existingMini(entries)
		}
		switch mini {
		case NoMiniEntry:
			r.Unconnected = append(r.Unconnected, agent)
		case MiniEntryServes:
			r.HasMini = append(r.HasMini, agent)
		default:
			r.InactiveMini = append(r.InactiveMini, agent)
		}
	}
}

// CatalogServer is the server config written for a catalog entry.
func CatalogServer(entry catalog.Entry) config.ServerConfig {
	sc := config.ServerConfig{Name: entry.Name, Transport: "http", URL: entry.URL}
	if entry.Auth == catalog.AuthOAuth2 && !sc.HasBundledAuth() {
		sc.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2}
	}
	return sc
}
