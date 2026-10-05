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
)

type ConnectChoice int

const (
	ConnectAndRemove ConnectChoice = iota
	ConnectOnly
	DontConnect
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
	Agent   agents.Agent
	Backup  string
	Created bool
	// MiniAlreadyConnected: the agent already ran mini, under any key; that entry is the user's and
	// is left as it is.
	MiniAlreadyConnected bool
	Removed              []string
	Kept                 []KeptEntry
	Changed              []string
	Err                  error
}

// KeptEntry is an agent entry mini duplicates but didn't replace, because mini's copy failed its
// connection check or was never checked.
type KeptEntry struct {
	Entry  string
	Server string
	Err    error
}

var errNotChecked = errors.New("its connection wasn't checked")

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
	result := p.edit(agent, mini)
	result.Changed = slices.DeleteFunc(slices.Clone(p.Counted[agent.Name]), func(entry string) bool {
		return result.Err != nil || slices.Contains(result.Removed, entry)
	})
	return result
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
		entries, err := agent.Read(agent.ConfigPath)
		if err != nil {
			return nil, err
		}
		result.MiniAlreadyConnected = runsMini(entries, p.SelfPath)
		result.Removed, result.Kept = p.replaceable(entries, mini)
		switch {
		case !result.MiniAlreadyConnected:
			return agent.Connect(config, result.Removed, &p.Mini)
		case len(result.Removed) == 0:
			return config, nil
		default:
			return agent.Connect(config, result.Removed, nil)
		}
	}
	result.Backup, result.Err = agents.EditFile(agent.ConfigPath, edit, p.Now)
	if result.Err != nil {
		result.Removed, result.Kept = nil, nil
	}
	return result
}

func runsMini(entries map[string]agents.Server, selfPath string) bool {
	for name, entry := range entries {
		if name == agents.MiniKey || agents.IsMiniEntry(entry.Config, selfPath) {
			return true
		}
	}
	return false
}

// Reads the entries again at apply, so an entry edited since Connect's check is judged as it is now.
func (p ApplyParams) replaceable(entries map[string]agents.Server, mini MiniServers) ([]string, []KeptEntry) {
	if p.Choice != ConnectAndRemove {
		return nil, nil
	}
	var remove []string
	var kept []KeptEntry
	duplicates := mini.Duplicates(entries, p.SelfPath)
	for _, entry := range slices.Sorted(maps.Keys(duplicates)) {
		if err := p.checkResult(duplicates[entry]); err != nil {
			kept = append(kept, KeptEntry{Entry: entry, Server: duplicates[entry], Err: err})
		} else {
			remove = append(remove, entry)
		}
	}
	return remove, kept
}

func (p ApplyParams) checkResult(server string) error {
	if err, checked := p.Checks[server]; checked {
		return err
	}
	return errNotChecked
}
