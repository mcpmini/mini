package initcmd

import (
	"context"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/server"
)

// ConnectPlan is what Connect knows before its connection checks: mini's servers and, per agent,
// the entries removing would replace if their mini copy passes its check.
type ConnectPlan struct {
	setup      Setup
	serversDir string
	mini       miniServers
	agents     map[string]agentDuplicates
}

type agentDuplicates struct {
	existing   ExistingMini
	duplicates map[string]string
}

// PlanConnect reads the staged servers: what removing replaces is what mini will have once Finish commits.
func (r *Run) PlanConnect() (ConnectPlan, error) {
	return r.setup.planConnect(r.stage.dir)
}

func (s Setup) planConnect(serversDir string) (ConnectPlan, error) {
	mini, err := loadMiniServers(serversDir)
	if err != nil {
		return ConnectPlan{}, err
	}
	p := ConnectPlan{setup: s, serversDir: serversDir, mini: mini, agents: map[string]agentDuplicates{}}
	rule := s.replacementRule(nil)
	for _, agent := range s.AgentsToConnect {
		entries, err := agent.Read(agent.ConfigPath)
		if err != nil {
			continue // nothing to remove from a missing config; apply reports one it can't read or parse
		}
		a := agentDuplicates{
			existing:   rule.entryCheck.existingMini(entries),
			duplicates: mini.Duplicates(entries, s.SelfPath),
		}
		if rule.mayRemove(a.existing, a.duplicates) {
			p.agents[agent.Name] = a
		}
	}
	return p, nil
}

// HasDuplicates reports whether the agent has entries that removing could replace with mini.
func (p ConnectPlan) HasDuplicates(agent string) bool {
	_, ok := p.agents[agent]
	return ok
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
	r.Checks = p.mini.Check(ctx, checkParams{
		configDir: p.serversDir,
		servers:   duplicatedServers(p.allDuplicates()...),
		clock:     clock.System(),
		probe:     p.setup.probe(),
	})
	rule := p.setup.replacementRule(r.Checks)
	for agent, a := range p.agents {
		r.ByAgent[agent], _ = rule.splitDuplicates(a.existing, a.duplicates)
	}
	return r
}

func (p ConnectPlan) allDuplicates() []map[string]string {
	var all []map[string]string
	for _, a := range p.agents {
		all = append(all, a.duplicates)
	}
	return all
}

func (s Setup) probe() probeFunc {
	if s.Probe != nil {
		return s.Probe
	}
	return server.ProbeServer
}
