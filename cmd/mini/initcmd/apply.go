package initcmd

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/mcpmini/mini/internal/agents"
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
	Removed      []string
	Kept         []KeptEntry
	Changed      []string
	Err          error
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
	errMiniInactive = errors.New("the agent's mini entry may not run these servers: it's switched off, uses another config directory, or doesn't start mini connect by absolute path")
)

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
		return AgentResult{Agent: agent, Created: err == nil, Err: err}
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
		result.Removed, result.Kept, result.Changed = nil, nil, nil
	}
	return result
}

// Reads the entries again at apply, so an entry edited since Connect's check is judged as it is now.
func (p ApplyParams) editedConfig(agent agents.Agent, mini MiniServers, config []byte, result *AgentResult) ([]byte, error) {
	entries, err := agent.Read(agent.ConfigPath)
	if err != nil {
		return nil, err
	}
	result.ExistingMini = p.existingMini(entries)
	if p.Choice == ConnectAndRemove {
		duplicates := mini.Duplicates(entries, p.SelfPath)
		result.Changed = changedSince(p.Counted[agent.Name], duplicates)
		result.Removed, result.Kept = p.replaceable(duplicates, p.servedAfterEdit(result.ExistingMini))
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

func changedSince(counted []string, duplicates map[string]string) []string {
	var changed []string
	for _, entry := range counted {
		if _, still := duplicates[entry]; !still {
			changed = append(changed, entry)
		}
	}
	return changed
}

// A duplicate goes only when the agent ends up with an enabled mini serving the servers checked
// here: its own entry, or the one init writes.
func (p ApplyParams) servedAfterEdit(existing ExistingMini) bool {
	switch existing {
	case MiniEntryServes:
		return true
	case NoMiniEntry:
		return p.serves(config.ServerConfig{Command: p.Mini.Command, Args: p.Mini.Args}, false)
	}
	return false
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
