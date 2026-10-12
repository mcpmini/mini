package initcmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"time"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/clock"
)

type ConnectChoice int

const (
	DontConnect ConnectChoice = iota
	ConnectOnly
	ConnectAndRemove
)

type applyParams struct {
	configDir string
	selfPath  string
	agents    []agents.Agent
	choice    ConnectChoice
	// counted is what Connect said it would remove, per agent name. An entry that is no longer a
	// duplicate at apply changed since, and is left alone.
	counted map[string][]string
	rule    replacementRule
	now     time.Time
}

type AgentResult struct {
	Agent        agents.Agent
	Backup       string
	Created      bool
	ExistingMini ExistingMini
	// MiniServes: the agent ends up with a switched-on mini entry for this config directory, its
	// own or the one init wrote; false means the agent won't get mini's servers through it.
	MiniServes bool
	Removed    []string
	Err        error
}

const inactiveMiniReasons = "it's switched off, uses another config directory, " +
	"or doesn't start mini connect by absolute path"

// ConnectParams is what the user chose on Connect.
type ConnectParams struct {
	Agents   []agents.Agent
	Choice   ConnectChoice
	Removals Removals
}

func (s Setup) connectAgents(ctx context.Context, p ConnectParams) []AgentResult {
	return apply(ctx, applyParams{
		configDir: s.ConfigDir,
		selfPath:  s.SelfPath,
		agents:    p.Agents,
		choice:    p.Choice,
		counted:   p.Removals.ByAgent,
		rule:      s.replacementRule(p.Removals.Checks),
		now:       clock.System().Now(),
	})
}

// A failed agent doesn't stop the others; once ctx is cancelled no further agent is edited.
func apply(ctx context.Context, p applyParams) []AgentResult {
	if p.choice == DontConnect {
		return nil
	}
	mini, err := loadMiniServers(p.configDir)
	var results []AgentResult
	for _, agent := range p.agents {
		switch {
		case err != nil:
			results = append(results, AgentResult{Agent: agent, Err: err})
		case ctx.Err() != nil:
			results = append(results, AgentResult{Agent: agent, Err: ctx.Err()})
		default:
			results = append(results, p.connect(agent, mini))
		}
	}
	return results
}

func (p applyParams) connect(agent agents.Agent, mini miniServers) AgentResult {
	if _, err := os.Stat(agent.ConfigPath); errors.Is(err, fs.ErrNotExist) {
		err := p.create(agent)
		// A config the agent wrote since the check is the agent's, so it's edited like any other.
		if !errors.Is(err, fs.ErrExist) {
			return AgentResult{
				Agent:      agent,
				Created:    err == nil,
				MiniServes: err == nil && p.rule.servedAfterEdit(NoMiniEntry),
				Err:        err,
			}
		}
	}
	return p.edit(agent, mini)
}

func (p applyParams) create(agent agents.Agent) error {
	data, err := agent.Connect(nil, nil, &p.rule.miniToAdd)
	if err != nil {
		return err
	}
	return agents.CreateFile(agent.ConfigPath, data)
}

func (p applyParams) edit(agent agents.Agent, mini miniServers) AgentResult {
	result := AgentResult{Agent: agent}
	edit := func(config []byte) ([]byte, error) {
		result = AgentResult{Agent: agent}
		return p.editedConfig(agent, mini, config, &result)
	}
	result.Backup, result.Err = agents.EditFile(agent.ConfigPath, edit, p.now)
	if result.Err != nil {
		result.MiniServes, result.Removed = false, nil
	}
	return result
}

// Judges the entries as they are at apply, so an entry edited since Connect's check is judged as it is now.
func (p applyParams) editedConfig(
	agent agents.Agent,
	mini miniServers,
	config []byte,
	result *AgentResult,
) ([]byte, error) {
	entries, err := agent.Parse(config)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", agent.ConfigPath, err)
	}
	result.ExistingMini = p.rule.entryCheck.existingMini(entries)
	result.MiniServes = p.rule.servedAfterEdit(result.ExistingMini)
	if p.choice == ConnectAndRemove {
		result.Removed = p.removals(agent.Name, result.ExistingMini, mini.Duplicates(entries, p.selfPath))
	}
	switch {
	case result.ExistingMini == NoMiniEntry:
		return agent.Connect(config, result.Removed, &p.rule.miniToAdd)
	case len(result.Removed) == 0:
		return config, nil
	default:
		return agent.Connect(config, result.Removed, nil)
	}
}

func (p applyParams) removals(agent string, existing ExistingMini, duplicates map[string]string) []string {
	// The user agreed only to what Connect counted; an entry removable since then stays.
	return slices.DeleteFunc(p.rule.removableDuplicates(existing, duplicates), func(entry string) bool {
		return !slices.Contains(p.counted[agent], entry)
	})
}
