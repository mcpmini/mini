package initcmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

type ConnectChoice int

const (
	DontConnect ConnectChoice = iota
	ConnectOnly
	ConnectAndRemove
)

type ApplyParams struct {
	ConfigDir string
	Agents    []agents.Agent
	Choice    ConnectChoice
	Mini      agents.MiniEntry
	SelfPath  string
	// Checks holds Connect's connection check per mini server; only servers that passed replace
	// an agent's entry.
	Checks map[string]error
	// Counted is what Connect said it would remove, per agent name. An entry that no longer
	// matches at apply changed since, and is left alone and reported.
	Counted map[string][]string
	Now     time.Time
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

var (
	errNotChecked   = errors.New("its connection wasn't checked")
	errMiniInactive = errors.New(
		"the agent's mini entry may not run these servers: it's switched off, uses another config directory, or doesn't start mini connect by absolute path",
	)
)

// ConnectParams is what the user chose on Connect.
type ConnectParams struct {
	Agents   []agents.Agent
	Choice   ConnectChoice
	Removals Removals
}

func (s Setup) Connect(ctx context.Context, p ConnectParams) []AgentResult {
	return Apply(ctx, s.applyParams(p))
}

func (s Setup) applyParams(p ConnectParams) ApplyParams {
	return ApplyParams{
		ConfigDir: s.ConfigDir,
		Agents:    p.Agents,
		Choice:    p.Choice,
		Mini:      MiniCommand(s.ConfigDir),
		SelfPath:  s.SelfPath,
		Checks:    p.Removals.Checks,
		Counted:   p.Removals.ByAgent,
		Now:       clock.System().Now(),
	}
}

// Apply connects mini to each agent in turn. A failed agent doesn't stop the others; once ctx is
// cancelled no further agent is edited.
func Apply(ctx context.Context, p ApplyParams) []AgentResult {
	if p.Choice == DontConnect {
		return nil
	}
	mini, err := LoadMiniServers(p.ConfigDir)
	var results []AgentResult
	for _, agent := range p.Agents {
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

func (p ApplyParams) connect(agent agents.Agent, mini MiniServers) AgentResult {
	if _, err := os.Stat(agent.ConfigPath); errors.Is(err, fs.ErrNotExist) {
		err := p.create(agent)
		// A config the agent wrote since the check is the agent's, so it's edited like any other.
		if !errors.Is(err, fs.ErrExist) {
			return AgentResult{
				Agent:      agent,
				Created:    err == nil,
				MiniServes: err == nil && p.servedAfterEdit(NoMiniEntry),
				Err:        err,
			}
		}
	}
	return p.edit(agent, mini)
}

func (p ApplyParams) create(agent agents.Agent) error {
	data, err := agent.Connect(nil, nil, &p.Mini)
	if err != nil {
		return err
	}
	return agents.CreateFile(agent.ConfigPath, data)
}

func (p ApplyParams) edit(agent agents.Agent, mini MiniServers) AgentResult {
	result := AgentResult{Agent: agent}
	edit := func(config []byte) ([]byte, error) {
		result = AgentResult{Agent: agent}
		return p.editedConfig(agent, mini, config, &result)
	}
	result.Backup, result.Err = agents.EditFile(agent.ConfigPath, edit, p.Now)
	if result.Err != nil {
		result.MiniServes, result.Removed, result.Kept, result.Changed = false, nil, nil, nil
	}
	return result
}

// Judges the entries as they are at apply, so an entry edited since Connect's check is judged as it is now.
func (p ApplyParams) editedConfig(
	agent agents.Agent,
	mini MiniServers,
	config []byte,
	result *AgentResult,
) ([]byte, error) {
	entries, err := agent.Parse(config)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", agent.ConfigPath, err)
	}
	result.ExistingMini = p.miniCheck().existingMini(entries)
	result.MiniServes = p.servedAfterEdit(result.ExistingMini)
	if p.Choice == ConnectAndRemove {
		duplicates := mini.Duplicates(entries, p.SelfPath)
		result.Changed = changedSince(p.Counted[agent.Name], entries, duplicates)
		result.Removed, result.Kept = p.replaceable(duplicates, result.MiniServes)
	}
	switch {
	case result.ExistingMini == NoMiniEntry:
		return agent.Connect(config, result.Removed, &p.Mini)
	case len(result.Removed) == 0:
		return config, nil
	default:
		return agent.Connect(config, result.Removed, nil)
	}
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

// A duplicate goes only when the agent ends up with a mini entry serving the servers checked here:
// its own, or the one init writes.
func (p ApplyParams) servedAfterEdit(existing ExistingMini) bool {
	if existing != NoMiniEntry {
		return existing == MiniEntryServes
	}
	return p.miniCheck().serves(agents.Server{Config: config.ServerConfig{Command: p.Mini.Command, Args: p.Mini.Args}})
}

func (p ApplyParams) replaceable(duplicates map[string]string, served bool) ([]string, []KeptEntry) {
	var remove []string
	var kept []KeptEntry
	for _, entry := range slices.Sorted(maps.Keys(duplicates)) {
		if err := p.keepReason(duplicates[entry], served); err != nil {
			kept = append(kept, KeptEntry{Entry: entry, Server: duplicates[entry], Err: err})
		} else {
			remove = append(remove, entry)
		}
	}
	return remove, kept
}

func (p ApplyParams) keepReason(server string, served bool) error {
	if !served {
		return errMiniInactive
	}
	if err, checked := p.Checks[server]; checked {
		return err
	}
	return errNotChecked
}

func (p ApplyParams) miniCheck() miniEntryCheck {
	return miniEntryCheck{configDir: p.ConfigDir, selfPath: p.SelfPath}
}
