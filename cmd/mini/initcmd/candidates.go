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
	Agent string
	Name  string
	Entry config.ServerConfig
}

type SkipReason int

const (
	SkipEmptyName SkipReason = iota
	SkipLimitsTools
	SkipRequiresApproval
	SkipUnsupported
)

// SkippedServer is an agent entry init leaves in the agent; the summary says why.
type SkippedServer struct {
	Agent    string
	Name     string
	Reason   SkipReason
	Settings []string
}

type FindParams struct {
	Agents     []agents.Agent
	Configured []config.ServerConfig
	// SelfPath is the running binary, so an entry that runs it is recognized as mini.
	SelfPath string
}

// The key Connect writes mini under; whatever an agent has there is replaced, never imported.
const miniKey = "mini"

type agentEntry struct {
	agent  string
	name   string
	server agents.Server
}

func FindServers(p FindParams) ([]Candidate, []SkippedServer) {
	configured := lowercaseNames(p.Configured)
	var entries []agentEntry
	var skipped []SkippedServer
	for _, agent := range p.Agents {
		servers, err := agent.Read(agent.ConfigPath)
		if err != nil {
			// TODO(#277): tell the user which agents couldn't be read and why.
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(servers)) {
			entry := agentEntry{agent: agent.Name, name: name, server: servers[name]}
			if !offered(entry, configured, p.SelfPath) {
				continue
			}
			if skip, ok := skipReason(entry); ok {
				skipped = append(skipped, skip)
				continue
			}
			entries = append(entries, entry)
		}
	}
	return mergeEntries(entries, configured), skipped
}

// mini's copy wins over an agent's for a configured name, whatever its config.
func offered(e agentEntry, configured map[string]bool, selfPath string) bool {
	name := NormalizeName(e.name)
	return name != miniKey && !configured[name] && !agents.IsMiniEntry(e.server.Config, selfPath)
}

func skipReason(e agentEntry) (SkippedServer, bool) {
	skip := SkippedServer{Agent: e.agent, Name: e.name}
	switch {
	case NormalizeName(e.name) == "":
		skip.Reason = SkipEmptyName
	case e.server.LimitsTools:
		skip.Reason = SkipLimitsTools
	case e.server.RequiresApproval:
		skip.Reason = SkipRequiresApproval
	case len(e.server.Unsupported) > 0:
		skip.Reason, skip.Settings = SkipUnsupported, e.server.Unsupported
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
