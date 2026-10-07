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

// RunningMini names the agents to connect whose mini entry already serves this config directory.
func (s Setup) RunningMini() map[string]bool {
	running := map[string]bool{}
	for _, agent := range ClassifyAgents(s.ConfigDir, s.SelfPath, s.AgentsToConnect).MiniServes {
		running[agent.Name] = true
	}
	return running
}

func (c miniEntryCheck) existingMiniIn(agent agents.Agent) ExistingMini {
	entries, err := agent.Read(agent.ConfigPath)
	if err != nil {
		return NoMiniEntry
	}
	return c.existingMini(entries)
}
