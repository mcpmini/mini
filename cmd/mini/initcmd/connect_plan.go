package initcmd

import (
	"context"
	"maps"
	"slices"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/server"
)

// ConnectPlan is what Connect knows before its connection checks: mini's servers and, per agent,
// the entries removing would replace if their mini copy passes its check.
type ConnectPlan struct {
	setup      Setup
	mini       MiniServers
	duplicates map[string]map[string]string
}

func (s Setup) PlanConnect() (ConnectPlan, error) {
	mini, err := LoadMiniServers(s.ConfigDir)
	if err != nil {
		return ConnectPlan{}, err
	}
	p := ConnectPlan{setup: s, mini: mini, duplicates: map[string]map[string]string{}}
	params := s.applyParams(ConnectParams{Choice: ConnectAndRemove})
	for _, agent := range s.AgentsToConnect {
		entries, err := agent.Read(agent.ConfigPath)
		if err != nil {
			continue // an agent with no config yet has nothing to remove
		}
		// An entry goes only when the agent ends up with a mini entry serving these servers.
		served := params.servedAfterEdit(params.miniCheck().existingMini(entries))
		if duplicates := mini.Duplicates(entries, s.SelfPath); served && len(duplicates) > 0 {
			p.duplicates[agent.Name] = duplicates
		}
	}
	return p, nil
}

// HasDuplicates reports whether the agent has entries that removing could replace with mini.
func (p ConnectPlan) HasDuplicates(agent string) bool {
	return len(p.duplicates[agent]) > 0
}

// Removals is what removing would do once the checks ran: each mini server's check, and per agent
// the entries whose mini copy passed.
type Removals struct {
	Checks  map[string]error
	ByAgent map[string][]string
}

// Check connects to every mini server an agent entry duplicates, and blocks until each finishes or times out.
func (p ConnectPlan) Check(ctx context.Context) Removals {
	r := Removals{ByAgent: map[string][]string{}}
	r.Checks = p.mini.Check(ctx, CheckParams{
		ConfigDir: p.setup.ConfigDir,
		Servers:   DuplicatedServers(slices.Collect(maps.Values(p.duplicates))...),
		Clock:     clock.System(),
		Probe:     p.setup.probe(),
	})
	params := p.setup.applyParams(ConnectParams{Choice: ConnectAndRemove, Removals: r})
	for agent, duplicates := range p.duplicates {
		r.ByAgent[agent], _ = params.replaceable(duplicates, true)
	}
	return r
}

// RemovalDisables reports whether removing an entry from the agent switches it off instead: Codex
// keeps it in its config, disabled.
func RemovalDisables(agent agents.Agent) bool {
	return agent.Name == "Codex"
}

func (s Setup) probe() probeFunc {
	if s.Probe != nil {
		return s.Probe
	}
	return server.ProbeServer
}
