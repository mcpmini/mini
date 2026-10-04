package initcmd

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"slices"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/agents"
	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
)

const connectCheckTimeout = 10 * time.Second

// ConnectableAgents are the agents Connect can offer: those whose config reads, and installed
// agents with no MCP config yet, whose file Connect creates.
func ConnectableAgents(known []agents.Agent) []agents.Agent {
	var connectable []agents.Agent
	for _, agent := range known {
		_, err := os.Stat(agent.ConfigPath)
		switch {
		case agent.ConfigPath == "":
		case err == nil:
			if _, err := agent.Read(agent.ConfigPath); err == nil {
				connectable = append(connectable, agent)
			}
		case errors.Is(err, fs.ErrNotExist) && isDir(agent.Dir):
			connectable = append(connectable, agent)
		}
	}
	return connectable
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// MiniServers holds mini's enabled servers twice: as written, to compare with agent entries
// (which hold ${VAR} unexpanded), and as loaded, to connect to.
type MiniServers struct {
	written []config.ServerConfig
	loaded  map[string]config.ServerConfig
}

func LoadMiniServers(configDir string) (MiniServers, error) {
	servers, err := config.LoadServers(configDir)
	if err != nil {
		return MiniServers{}, err
	}
	mini := MiniServers{loaded: map[string]config.ServerConfig{}}
	for _, sc := range servers.Loaded {
		written, err := UnexpandedServer(configDir, sc.Name)
		if !sc.IsEnabled() || err != nil {
			continue
		}
		mini.written = append(mini.written, written)
		mini.loaded[sc.Name] = sc
	}
	return mini, nil
}

// UnexpandedServer reads a server file as written, before ${VAR} expansion.
func UnexpandedServer(configDir, name string) (config.ServerConfig, error) {
	path := config.ServerPath(configDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return config.ServerConfig{}, err
	}
	var sc config.ServerConfig
	// yaml errors can quote the offending value, which may be a header token.
	if yaml.Unmarshal(data, &sc) != nil {
		return config.ServerConfig{}, errors.New(path + " does not parse")
	}
	sc.Name = name
	return sc, nil
}

// Duplicates pairs each agent entry init may replace with the mini server it duplicates. Entries
// that limit tools, ask for approval, are switched off, or are mini itself are never replaced.
func (m MiniServers) Duplicates(entries map[string]agents.Server, selfPath string) map[string]string {
	duplicates := map[string]string{}
	for name, entry := range entries {
		if name == agents.MiniKey || !entry.Candidate() || entry.Disabled || agents.IsMiniEntry(entry.Config, selfPath) {
			continue
		}
		i := slices.IndexFunc(m.written, func(sc config.ServerConfig) bool { return agents.SameServer(sc, entry.Config) })
		if i >= 0 {
			duplicates[name] = m.written[i].Name
		}
	}
	return duplicates
}

type CheckParams struct {
	ConfigDir string
	Servers   []string
	Clock     clock.Clock
	Probe     func(ctx context.Context, configDir string, sc config.ServerConfig) error
}

// Check connects to the named servers concurrently. A server passes when its error is nil; one
// missing from the result was never checked and counts as failed.
func (m MiniServers) Check(ctx context.Context, p CheckParams) map[string]error {
	var mu sync.Mutex
	var wg sync.WaitGroup
	results := make(map[string]error, len(p.Servers))
	for _, name := range p.Servers {
		if sc, ok := m.loaded[name]; ok {
			wg.Go(func() {
				err := p.check(ctx, sc)
				mu.Lock()
				defer mu.Unlock()
				results[name] = err
			})
		}
	}
	wg.Wait()
	return results
}

func (p CheckParams) check(ctx context.Context, sc config.ServerConfig) error {
	probeCtx, cancel := clock.WithTimeout(ctx, p.Clock, connectCheckTimeout)
	defer cancel()
	return p.Probe(probeCtx, p.ConfigDir, sc)
}

// DuplicatedServers lists the mini servers that Connect must check: those some agent entry duplicates.
func DuplicatedServers(duplicates ...map[string]string) []string {
	var names []string
	for _, d := range duplicates {
		for name := range maps.Values(d) {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return names
}
