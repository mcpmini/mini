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
	// FromImport are --add names an imported server already covers, so the catalog's isn't added.
	FromImport []string
	// Connected holds one result per agent Connect touched; nil when nothing was connected.
	Connected []AgentResult
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
	p.sortByMini(&report)
	configured, err := writtenServers(p.ConfigDir)
	if err != nil {
		report.StatusErr = err
		return report
	}
	candidates, skipped := FindServers(FindParams{Agents: p.Import, Configured: configured, SelfPath: p.SelfPath})
	report.Skipped = append(skipped, leftOutRows(candidates)...)
	report.Ignored, report.UnusedEnvHeaders = ignoredSettings(candidates), unusedEnvHeaders(candidates)
	picks := flagPicks(candidates, p.Add, configured)
	report.AlreadyConfigured, report.FromImport = picks.already, picks.fromImport
	session := NewSession(SessionParams{ConfigDir: p.ConfigDir})
	report.Sync = session.Sync(picks.want)
	session.WaitChecks()
	report.dropNotesOfFailedServers()
	report.Servers, report.StatusErr = ServerStatuses(p.ConfigDir, p.Catalog)
	return report
}

func (r *Report) dropNotesOfFailedServers() {
	for _, failure := range r.Sync.Failed {
		delete(r.Ignored, failure.Name)
		delete(r.UnusedEnvHeaders, failure.Name)
	}
}

type picks struct {
	want       []config.ServerConfig
	already    []string
	fromImport []string
}

// Flags take each row's default tick, so switched-off servers and second configs stay out. An
// imported server wins over a catalog server it matches.
func flagPicks(candidates []Candidate, add []catalog.Entry, configured []config.ServerConfig) picks {
	var p picks
	for _, c := range candidates {
		if c.Checked {
			p.want = append(p.want, c.Config)
		}
	}
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

// Flags leave out each row the UI starts unticked: one every agent switched off, which importing
// would switch on for every agent connected to mini, and a second config under a name in use.
func leftOutRows(candidates []Candidate) []SkippedServer {
	var left []SkippedServer
	for _, c := range candidates {
		if c.Checked {
			continue
		}
		reason := SkipSecondConfig
		if !slices.ContainsFunc(c.Sources, func(s Source) bool { return !s.Disabled }) {
			reason = SkipSwitchedOff
		}
		for _, s := range c.Sources {
			left = append(left, SkippedServer{Agent: s.Agent, Name: s.Name, Reason: reason})
		}
	}
	return left
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

func unusedEnvHeaders(candidates []Candidate) map[string]map[string]string {
	unused := map[string]map[string]string{}
	for _, c := range candidates {
		if !c.Checked {
			continue
		}
		for _, source := range c.Sources {
			for header, envVar := range source.UnusedEnvHeaders {
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
