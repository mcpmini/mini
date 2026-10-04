package initcmd

import (
	"slices"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/catalog"
	"github.com/mcpmini/mini/internal/config"
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
	ConfigDir string
	Servers   []ServerStatus
	Sync      SyncResult
	Skipped   []SkippedServer
	// Ignored holds, per imported server, the agent settings mini doesn't carry over.
	Ignored map[string][]string
	// UnusedEnvHeaders holds, per imported server, each static header kept over the variable the
	// agent would read it from once that is set.
	UnusedEnvHeaders  map[string]map[string]string
	AlreadyConfigured []string
	// Connected holds one result per agent Connect touched; nil when nothing was connected.
	Connected []AgentResult
	// Unconnected are agents the summary shows how to connect by hand.
	Unconnected []agents.Agent
	// HasMini are agents that already have a mini entry; it is the user's, so they get no step.
	HasMini   []agents.Agent
	Mini      agents.MiniEntry
	StatusErr error
}

func (r Report) Failed() bool {
	if len(r.Sync.Failed) > 0 || r.StatusErr != nil {
		return true
	}
	for _, result := range r.Connected {
		if result.Err != nil {
			return true
		}
	}
	return false
}

func RunFlags(p FlagRun) Report {
	report := Report{ConfigDir: p.ConfigDir, Mini: MiniCommand(p.ConfigDir)}
	report.Unconnected, report.HasMini = p.splitByMini()
	configured, err := writtenServers(p.ConfigDir)
	if err != nil {
		report.StatusErr = err
		return report
	}
	candidates, skipped := FindServers(FindParams{Agents: p.Import, Configured: configured, SelfPath: p.SelfPath})
	report.Skipped, report.Ignored, report.UnusedEnvHeaders = skipped, ignoredSettings(
		candidates,
	), unusedEnvHeaders(
		candidates,
	)
	want, already := flagPicks(candidates, p.Add, configured)
	report.AlreadyConfigured = already
	session := NewSession(SessionParams{ConfigDir: p.ConfigDir})
	report.Sync = session.Sync(want)
	session.WaitChecks()
	report.Servers, report.StatusErr = ServerStatuses(p.ConfigDir, p.Catalog)
	return report
}

// Flags take each row's default tick, so switched-off servers and second configs stay out. An
// imported server wins over a catalog server it matches.
func flagPicks(
	candidates []Candidate,
	add []catalog.Entry,
	configured []config.ServerConfig,
) ([]config.ServerConfig, []string) {
	var want []config.ServerConfig
	for _, c := range candidates {
		if c.Checked {
			want = append(want, c.Config)
		}
	}
	hidden := NewConfiguredKeys(configured)
	picked := NewConfiguredKeys(want)
	var already []string
	for _, entry := range add {
		switch {
		case hidden.Has(entry):
			already = append(already, entry.Name)
		case !picked.Has(entry):
			want = append(want, CatalogServer(entry))
		}
	}
	return want, already
}

func (p FlagRun) splitByMini() (unconnected, hasMini []agents.Agent) {
	existing := ApplyParams{ConfigDir: p.ConfigDir, SelfPath: p.SelfPath}
	for _, agent := range p.Connectable {
		entries, err := agent.Read(agent.ConfigPath)
		if err == nil && existing.existingMini(entries) != NoMiniEntry {
			hasMini = append(hasMini, agent)
		} else {
			unconnected = append(unconnected, agent)
		}
	}
	return unconnected, hasMini
}

func unusedEnvHeaders(candidates []Candidate) map[string]map[string]string {
	unused := map[string]map[string]string{}
	for _, c := range candidates {
		for _, source := range c.Sources {
			for header, envVar := range source.UnusedEnvHeaders {
				if !c.Checked {
					continue
				}
				if unused[c.Name] == nil {
					unused[c.Name] = map[string]string{}
				}
				unused[c.Name][header] = envVar
			}
		}
	}
	return unused
}

func ignoredSettings(candidates []Candidate) map[string][]string {
	ignored := map[string][]string{}
	for _, c := range candidates {
		for _, source := range c.Sources {
			for _, setting := range source.IgnoredRunSettings {
				if c.Checked && !slices.Contains(ignored[c.Name], setting) {
					ignored[c.Name] = append(ignored[c.Name], setting)
				}
			}
		}
	}
	return ignored
}

// CatalogServer is the server config written for a catalog entry.
func CatalogServer(entry catalog.Entry) config.ServerConfig {
	sc := config.ServerConfig{Name: entry.Name, Transport: "http", URL: entry.URL}
	if entry.Auth == catalog.AuthOAuth2 && !sc.HasBundledAuth() {
		sc.Auth = &config.AuthConfig{Type: config.AuthTypeOAuth2}
	}
	return sc
}
