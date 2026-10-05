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

var (
	errNotChecked    = errors.New("its connection wasn't checked")
	errMiniElsewhere = errors.New("the agent's mini entry is switched off or runs another config directory")
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
		existing := p.existingMini(entries)
		result.MiniAlreadyConnected = existing != noMini
		result.Removed, result.Kept = p.replaceable(entries, mini, existing)
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

// Reads the entries again at apply, so an entry edited since Connect's check is judged as it is now.
func (p ApplyParams) replaceable(entries map[string]agents.Server, mini MiniServers, existing existingMini) ([]string, []KeptEntry) {
	if p.Choice != ConnectAndRemove {
		return nil, nil
	}
	var remove []string
	var kept []KeptEntry
	duplicates := mini.Duplicates(entries, p.SelfPath)
	for _, entry := range slices.Sorted(maps.Keys(duplicates)) {
		if err := p.keepReason(duplicates[entry], existing); err != nil {
			kept = append(kept, KeptEntry{Entry: entry, Server: duplicates[entry], Err: err})
		} else {
			remove = append(remove, entry)
		}
	}
	return remove, kept
}

// A duplicate goes only when the agent ends up with an enabled mini serving the servers checked
// here; a mini entry init leaves alone may be switched off or run another config directory.
func (p ApplyParams) keepReason(server string, existing existingMini) error {
	if existing == miniElsewhere {
		return errMiniElsewhere
	}
	if err, checked := p.Checks[server]; checked {
		return err
	}
	return errNotChecked
}
