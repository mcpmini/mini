package initcmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
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
	agents []agents.Agent
	choice ConnectChoice
	// counted is what Connect said it would remove, per agent name. An entry that no longer
	// matches at apply changed since, and is left alone and reported.
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
	Kept       []KeptEntry
	Changed    []string
	Err        error
}

// KeptEntry is an agent entry mini duplicates but didn't replace: mini's copy failed its
// connection check or was never checked, or the agent's mini entry won't serve it.
type KeptEntry struct {
	Entry  string
	Server string
	Err    error
}

const inactiveMiniReasons = "it's switched off, uses another config directory, " +
	"or doesn't start mini connect by absolute path"

var (
	errNotChecked   = errors.New("its connection wasn't checked")
	errNotShown     = errors.New("it wasn't listed for removal")
	errMiniInactive = errors.New("the agent's mini entry may not run these servers: " + inactiveMiniReasons)
)

// ConnectParams is what the user chose on Connect.
type ConnectParams struct {
	Agents   []agents.Agent
	Choice   ConnectChoice
	Removals Removals
}

func (s Setup) connectAgents(ctx context.Context, p ConnectParams) []AgentResult {
	return apply(ctx, applyParams{
		agents:  p.Agents,
		choice:  p.Choice,
		counted: p.Removals.ByAgent,
		rule:    s.replacementRule(p.Removals.Checks),
		now:     clock.System().Now(),
	})
}

func apply(ctx context.Context, p applyParams) []AgentResult {
	if p.choice == DontConnect {
		return nil
	}
	mini, err := loadMiniServers(p.rule.mini.configDir)
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
		result.MiniServes, result.Removed, result.Kept, result.Changed = false, nil, nil, nil
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
	result.ExistingMini = p.rule.mini.existingMini(entries)
	result.MiniServes = p.rule.servedAfterEdit(result.ExistingMini)
	if p.choice == ConnectAndRemove {
		duplicates := mini.Duplicates(entries, p.rule.mini.selfPath)
		result.Changed = changedSince(p.counted[agent.Name], entries, duplicates)
		result.Removed, result.Kept = p.removals(agent.Name, result.ExistingMini, duplicates)
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

func (p applyParams) removals(
	agent string,
	existing ExistingMini,
	duplicates map[string]string,
) ([]string, []KeptEntry) {
	removable, kept := p.rule.splitDuplicates(existing, duplicates)
	var remove []string
	for _, entry := range removable {
		// The user agreed only to what Connect listed; an entry removable since then stays.
		if slices.Contains(p.counted[agent], entry) {
			remove = append(remove, entry)
		} else {
			kept = append(kept, KeptEntry{Entry: entry, Server: duplicates[entry], Err: errNotShown})
		}
	}
	slices.SortFunc(kept, func(a, b KeptEntry) int { return strings.Compare(a.Entry, b.Entry) })
	return remove, kept
}

func changedSince(counted []string, entries map[string]agents.Server, duplicates map[string]string) []string {
	var changed []string
	for _, entry := range counted {
		_, exists := entries[entry] // one the user deleted since the check is gone either way
		if _, still := duplicates[entry]; exists && !still {
			changed = append(changed, entry)
		}
	}
	return changed
}
