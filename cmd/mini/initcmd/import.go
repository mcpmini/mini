package initcmd

import (
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

// ImportPlan is what importing the agents' servers writes to mini, and what it leaves in them.
type ImportPlan struct {
	Servers []config.ServerConfig
	Skipped []SkippedServer
	// Ignored holds, per imported server, the agent settings mini doesn't carry over.
	Ignored map[string][]string
	// UnusedEnvHeaders holds, per imported server, each static header kept over the variable the
	// agent would read it from once that is set.
	UnusedEnvHeaders map[string]map[string]string
}

type SkipReason int

const (
	SkipEmptyName SkipReason = iota
	SkipUnexpandableRefs
	SkipSwitchedOff
	SkipSecondConfig
	SkipNameInMini
)

// SkippedServer is an agent entry init leaves in the agent; the summary says why.
type SkippedServer struct {
	Agent  string
	Name   string
	Reason SkipReason
	Refs   []string
}

type ImportParams struct {
	Agents []agents.Agent
	// Configured holds mini's servers as written, before ${VAR} expansion, so they compare with
	// agent entries, which hold references unexpanded too.
	Configured []config.ServerConfig
	// SelfPath is the running binary, so an entry that runs it is recognized as mini.
	SelfPath string
}

type agentEntry struct {
	agent  string
	name   string
	server agents.Server
}

// serverGroup is one server config found in one or more agents.
type serverGroup struct {
	name    string
	config  config.ServerConfig
	entries []agentEntry
}

func PlanImport(p ImportParams) ImportPlan {
	plan := ImportPlan{Ignored: map[string][]string{}, UnusedEnvHeaders: map[string]map[string]string{}}
	var groups []*serverGroup
	for _, e := range p.importable(&plan) {
		groups = addToGroup(groups, e)
	}
	// Groups keep agent order, which decides which config keeps a shared name.
	claimed := map[string]bool{}
	for _, g := range groups {
		plan.add(g, claimed)
	}
	slices.SortFunc(plan.Servers, func(a, b config.ServerConfig) int { return strings.Compare(a.Name, b.Name) })
	return plan
}

func (p ImportParams) importable(plan *ImportPlan) []agentEntry {
	configuredNames := lowercaseNames(p.Configured)
	var importable []agentEntry
	for _, agent := range p.Agents {
		servers, err := agent.Read(agent.ConfigPath)
		if err != nil {
			// TODO(#277): tell the user which agents couldn't be read and why.
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(servers)) {
			e := agentEntry{agent: agent.Name, name: name, server: servers[name]}
			switch skip, ok := skipReason(e, configuredNames); {
			case !p.offered(e):
			case ok:
				plan.Skipped = append(plan.Skipped, skip)
			default:
				importable = append(importable, e)
			}
		}
	}
	return importable
}

// Whatever an agent has under mini's own key, mini itself, and a server mini already has under
// any name are left out without a word: there's nothing for the user to do about them.
func (p ImportParams) offered(e agentEntry) bool {
	return NormalizeName(e.name) != agents.MiniKey && !agents.IsMiniEntry(e.server.Config, p.SelfPath) &&
		!slices.ContainsFunc(
			p.Configured,
			func(sc config.ServerConfig) bool { return agents.SameServer(sc, e.server.Config) },
		)
}

func skipReason(e agentEntry, configuredNames map[string]bool) (SkippedServer, bool) {
	skip := SkippedServer{Agent: e.agent, Name: e.name}
	switch {
	case configuredNames[NormalizeName(e.name)]:
		skip.Reason = SkipNameInMini
	case NormalizeName(e.name) == "":
		skip.Reason = SkipEmptyName
	case !e.server.Candidate():
		skip.Reason, skip.Refs = SkipUnexpandableRefs, e.server.UnexpandableRefs
	default:
		return SkippedServer{}, false
	}
	return skip, true
}

// One server under several names is imported once: twice would expose its tools twice.
func addToGroup(groups []*serverGroup, e agentEntry) []*serverGroup {
	i := slices.IndexFunc(groups, func(g *serverGroup) bool { return agents.SameServer(g.config, e.server.Config) })
	if i >= 0 {
		groups[i].entries = append(groups[i].entries, e)
		return groups
	}
	return append(groups, &serverGroup{name: NormalizeName(e.name), config: e.server.Config, entries: []agentEntry{e}})
}

func (g *serverGroup) enabled() bool {
	return slices.ContainsFunc(g.entries, func(e agentEntry) bool { return !e.server.Disabled })
}

func (plan *ImportPlan) add(g *serverGroup, claimed map[string]bool) {
	switch {
	case !g.enabled():
		// Importing a server every agent switched off would switch it on for every agent connected
		// to mini. It claims no name, so an enabled config elsewhere keeps the name.
		plan.skipAll(g, SkipSwitchedOff)
	case claimed[g.name]:
		// Another config under a name in use would need a new name the user never chose.
		plan.skipAll(g, SkipSecondConfig)
	default:
		claimed[g.name] = true
		sc := g.config
		sc.Name = g.name
		plan.Servers = append(plan.Servers, sc)
		plan.noteCaveats(g)
	}
}

func (plan *ImportPlan) skipAll(g *serverGroup, reason SkipReason) {
	for _, e := range g.entries {
		plan.Skipped = append(plan.Skipped, SkippedServer{Agent: e.agent, Name: e.name, Reason: reason})
	}
}

func (plan *ImportPlan) noteCaveats(g *serverGroup) {
	for _, e := range g.entries {
		for _, setting := range e.server.IgnoredRunSettings {
			if !slices.Contains(plan.Ignored[g.name], setting) {
				plan.Ignored[g.name] = append(plan.Ignored[g.name], setting)
			}
		}
		for header, envVar := range e.server.UnusedEnvHeaders {
			if plan.UnusedEnvHeaders[g.name] == nil {
				plan.UnusedEnvHeaders[g.name] = map[string]string{}
			}
			plan.UnusedEnvHeaders[g.name][header] = envVar
		}
	}
}

var outsideServerName = regexp.MustCompile(`[^a-z0-9_-]+`)

// NormalizeName gives an agent's server name as mini stores it; empty when nothing usable is left.
func NormalizeName(name string) string {
	return strings.Trim(outsideServerName.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

func lowercaseNames(servers []config.ServerConfig) map[string]bool {
	names := make(map[string]bool, len(servers))
	for _, sc := range servers {
		names[strings.ToLower(sc.Name)] = true
	}
	return names
}
