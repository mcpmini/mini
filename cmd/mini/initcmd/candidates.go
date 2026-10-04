// Package initcmd holds mini init's logic: what to offer, what to write, and what happened.
// It returns data and never prints; the command and the UI present it.
package initcmd

import (
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/config"
)

// Candidate is one Import row: a server found in one or more agents, under the name mini would give it.
type Candidate struct {
	Name    string
	Config  config.ServerConfig
	Sources []Source
	// Checked is the row's starting tick: off when every agent switched it off, or when it is a
	// second config under a name another row already has.
	Checked bool
}

// Source is the agent entry a candidate came from, as read, so apply can tell when it changed since.
type Source struct {
	Agent              string
	Name               string
	Entry              config.ServerConfig
	IgnoredRunSettings []string
	UnusedEnvHeaders   map[string]string
}

type SkipReason int

const (
	SkipEmptyName SkipReason = iota
	SkipUnexpandableRefs
)

// SkippedServer is an agent entry init leaves in the agent; the summary says why.
type SkippedServer struct {
	Agent  string
	Name   string
	Reason SkipReason
	Refs   []string
}

type FindParams struct {
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

type finder struct {
	FindParams
	configuredNames map[string]bool
	entries         []agentEntry
	skipped         []SkippedServer
}

func FindServers(p FindParams) ([]Candidate, []SkippedServer) {
	f := finder{FindParams: p, configuredNames: lowercaseNames(p.Configured)}
	for _, agent := range p.Agents {
		servers, err := agent.Read(agent.ConfigPath)
		if err != nil {
			// TODO(#277): tell the user which agents couldn't be read and why.
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(servers)) {
			f.add(agentEntry{agent: agent.Name, name: name, server: servers[name]})
		}
	}
	return mergeEntries(f.entries, f.configuredNames), f.skipped
}

func (f *finder) add(e agentEntry) {
	if !f.offered(e) {
		return
	}
	if skip, ok := skipReason(e); ok {
		f.skipped = append(f.skipped, skip)
		return
	}
	f.entries = append(f.entries, e)
}

// mini's copy wins over an agent's for a configured name, whatever its config, and for a
// configured server under another name. Whatever an agent has under mini's own key is replaced
// by Connect, never imported.
func (f *finder) offered(e agentEntry) bool {
	name := NormalizeName(e.name)
	return name != agents.MiniKey && !f.configuredNames[name] && !agents.IsMiniEntry(e.server.Config, f.SelfPath) &&
		!slices.ContainsFunc(f.Configured, func(sc config.ServerConfig) bool { return agents.SameServer(sc, e.server.Config) })
}

func skipReason(e agentEntry) (SkippedServer, bool) {
	skip := SkippedServer{Agent: e.agent, Name: e.name}
	switch {
	case NormalizeName(e.name) == "":
		skip.Reason = SkipEmptyName
	case !e.server.Candidate():
		skip.Reason, skip.Refs = SkipUnexpandableRefs, e.server.UnexpandableRefs
	default:
		return SkippedServer{}, false
	}
	return skip, true
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
