package initcmd

import "github.com/mcpmini/mini/internal/agents"

// AgentConnections sorts agents by the mini entry they already have. That entry is the user's,
// so only agents with none get a step to connect mini by hand.
type AgentConnections struct {
	NoMini       []agents.Agent
	MiniServes   []agents.Agent
	MiniInactive []agents.Agent
	// Mini is the entry the agents should run.
	Mini agents.MiniEntry
}

func ClassifyAgents(configDir, selfPath string, list []agents.Agent) AgentConnections {
	c := AgentConnections{Mini: MiniCommand(configDir)}
	check := miniEntryCheck{configDir: configDir, selfPath: selfPath}
	for _, agent := range list {
		switch check.existingMiniIn(agent) {
		case NoMiniEntry:
			c.NoMini = append(c.NoMini, agent)
		case MiniEntryServes:
			c.MiniServes = append(c.MiniServes, agent)
		default:
			c.MiniInactive = append(c.MiniInactive, agent)
		}
	}
	return c
}

func (c miniEntryCheck) existingMiniIn(agent agents.Agent) ExistingMini {
	entries, err := agent.Read(agent.ConfigPath)
	if err != nil {
		return NoMiniEntry
	}
	return c.existingMini(entries)
}
