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
	Servers    []config.ServerConfig
	Skipped    []SkippedServer
	Unreadable []UnreadableAgent
	// DroppedSettings holds, per imported server, the agent settings mini doesn't carry over.
	DroppedSettings map[string][]string
	// StaticHeaders holds, per imported server, each static header kept over the variable the
	// agent would read it from once that is set.
	StaticHeaders map[string]map[string]string
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

// UnreadableAgent is an agent whose config couldn't be read, so none of its servers were imported.
type UnreadableAgent struct {
	Agent      string
	ConfigPath string
	Err        error
}

type ImportParams struct {
	Agents   []agents.Agent
	Written  WrittenServers
	SelfPath string
}

type agentEntry struct {
	agent  string
	name   string
	server agents.Server
}

type serverGroup struct {
	config  config.ServerConfig
	entries []agentEntry
}

type takenNames struct {
	inMini   map[string]bool
	imported map[string]bool
}

func PlanImport(p ImportParams) ImportPlan {
	plan := ImportPlan{DroppedSettings: map[string][]string{}, StaticHeaders: map[string]map[string]string{}}
	var groups []*serverGroup
	for _, e := range p.readCandidates(&plan) {
		groups = addToGroup(groups, e)
	}
	// Groups keep agent order, which decides which config keeps a shared name.
	taken := takenNames{inMini: lowercaseNames(p.Written), imported: map[string]bool{}}
	for _, g := range groups {
		plan.add(g, taken)
	}
	slices.SortFunc(plan.Servers, func(a, b config.ServerConfig) int { return strings.Compare(a.Name, b.Name) })
	return plan
}

func (p ImportParams) readCandidates(plan *ImportPlan) []agentEntry {
	var candidates []agentEntry
	for _, agent := range p.Agents {
		servers, err := agent.Read(agent.ConfigPath)
		if err != nil {
			plan.Unreadable = append(
				plan.Unreadable,
				UnreadableAgent{Agent: agent.Name, ConfigPath: agent.ConfigPath, Err: err},
			)
			continue
		}
		candidates = append(candidates, p.candidatesIn(plan, agent.Name, servers)...)
	}
	return candidates
}

func (p ImportParams) candidatesIn(plan *ImportPlan, agent string, servers map[string]agents.Server) []agentEntry {
	var candidates []agentEntry
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		e := agentEntry{agent: agent, name: name, server: servers[name]}
		if !p.newToMini(e) {
			continue
		}
		if !e.server.Candidate() {
			plan.Skipped = append(plan.Skipped, SkippedServer{
				Agent: e.agent, Name: e.name, Reason: SkipUnexpandableRefs, Refs: e.server.UnexpandableRefs,
			})
			continue
		}
		candidates = append(candidates, e)
	}
	return candidates
}

// Left out with no summary line: there's nothing for the user to do about these.
func (p ImportParams) newToMini(e agentEntry) bool {
	return NormalizeName(e.name) != agents.MiniKey && !agents.IsMiniEntry(e.server.Config, p.SelfPath) &&
		!p.Written.hasSame(e.server.Config)
}

// One server under several names is imported once: twice would expose its tools twice.
func addToGroup(groups []*serverGroup, e agentEntry) []*serverGroup {
	i := slices.IndexFunc(groups, func(g *serverGroup) bool { return agents.SameServer(g.config, e.server.Config) })
	if i >= 0 {
		groups[i].entries = append(groups[i].entries, e)
		return groups
	}
	return append(groups, &serverGroup{config: e.server.Config, entries: []agentEntry{e}})
}

func (g *serverGroup) enabled() bool {
	return slices.ContainsFunc(g.entries, func(e agentEntry) bool { return !e.server.Disabled })
}

func (plan *ImportPlan) add(g *serverGroup, taken takenNames) {
	if !g.enabled() {
		// Importing a server every agent switched off would switch it on for every agent connected
		// to mini.
		plan.skipAll(g, func(agentEntry) SkipReason { return SkipSwitchedOff })
		return
	}
	name, ok := taken.firstFree(g)
	if !ok {
		// Another config under a name in use would need a new name the user never chose.
		plan.skipAll(g, taken.noFreeNameReason)
		return
	}
	taken.imported[name] = true
	sc := g.config
	sc.Name = name
	plan.Servers = append(plan.Servers, sc)
	plan.noteCaveats(name, g)
}

func (t takenNames) firstFree(g *serverGroup) (string, bool) {
	for _, e := range g.entries {
		if name := NormalizeName(e.name); name != "" && !t.inMini[name] && !t.imported[name] {
			return name, true
		}
	}
	return "", false
}

func (t takenNames) noFreeNameReason(e agentEntry) SkipReason {
	switch name := NormalizeName(e.name); {
	case name == "":
		return SkipEmptyName
	case t.inMini[name]:
		return SkipNameInMini
	}
	return SkipSecondConfig
}

func (plan *ImportPlan) skipAll(g *serverGroup, reason func(agentEntry) SkipReason) {
	for _, e := range g.entries {
		plan.Skipped = append(plan.Skipped, SkippedServer{Agent: e.agent, Name: e.name, Reason: reason(e)})
	}
}

func (plan *ImportPlan) noteCaveats(name string, g *serverGroup) {
	for _, e := range g.entries {
		for _, setting := range e.server.IgnoredRunSettings {
			if !slices.Contains(plan.DroppedSettings[name], setting) {
				plan.DroppedSettings[name] = append(plan.DroppedSettings[name], setting)
			}
		}
		for header, envVar := range e.server.UnusedEnvHeaders {
			if plan.StaticHeaders[name] == nil {
				plan.StaticHeaders[name] = map[string]string{}
			}
			plan.StaticHeaders[name][header] = envVar
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
