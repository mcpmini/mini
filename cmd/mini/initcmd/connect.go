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

// miniServers holds mini's enabled servers twice: as written, to compare with agent entries
// (which hold ${VAR} unexpanded), and as loaded, to connect to from the config dir that holds them.
type miniServers struct {
	written    []config.ServerConfig
	loaded     map[string]config.ServerConfig
	configDirs map[string]string
}

// loadMiniServers reads the servers of each config dir; a run's stage is read with mini's.
func loadMiniServers(configDirs ...string) (miniServers, error) {
	mini := miniServers{loaded: map[string]config.ServerConfig{}, configDirs: map[string]string{}}
	for _, configDir := range configDirs {
		if err := mini.load(configDir); err != nil {
			return miniServers{}, err
		}
	}
	return mini, nil
}

func (m *miniServers) load(configDir string) error {
	servers, err := config.LoadServers(configDir)
	if err != nil {
		return err
	}
	for _, sc := range servers.Loaded {
		written, err := config.ReadUnexpandedServer(configDir, sc.Name)
		if !sc.IsEnabled() || err != nil { // a server left out here only keeps its duplicates in the agents
			continue
		}
		m.written = append(m.written, written)
		m.loaded[sc.Name] = sc
		m.configDirs[sc.Name] = configDir
	}
	return nil
}

// Duplicates pairs each agent entry init may replace with the mini server it duplicates. Entries
// that are switched off, weren't importable, or run mini are never replaced. Tool limits and
// approval settings aren't carried into mini; a replaced entry keeps them only in the backup.
func (m miniServers) Duplicates(entries map[string]agents.Server, selfPath string) map[string]string {
	duplicates := map[string]string{}
	for name, entry := range entries {
		if name == agents.MiniKey || !entry.Candidate() || entry.Disabled ||
			agents.IsMiniEntry(entry.Config, selfPath) {
			continue
		}
		i := slices.IndexFunc(
			m.written,
			func(sc config.ServerConfig) bool { return agents.SameServer(sc, entry.Config) },
		)
		if i >= 0 {
			duplicates[name] = m.written[i].Name
		}
	}
	return duplicates
}

type checkParams struct {
	servers []string
	clock   clock.Clock
	probe   probeFunc
}

// Check connects to the named servers concurrently. A server passes when its error is nil; one
// missing from the result was never checked and counts as failed.
func (m miniServers) Check(ctx context.Context, p checkParams) map[string]error {
	var mu sync.Mutex
	var wg sync.WaitGroup
	results := make(map[string]error, len(p.servers))
	for _, name := range p.servers {
		if sc, ok := m.loaded[name]; ok {
			wg.Go(func() {
				err := p.check(ctx, m.configDirs[name], sc)
				mu.Lock()
				defer mu.Unlock()
				results[name] = err
			})
		}
	}
	wg.Wait()
	return results
}

func (p checkParams) check(ctx context.Context, configDir string, sc config.ServerConfig) error {
	probeCtx, cancel := clock.WithTimeout(ctx, p.clock, connectCheckTimeout)
	defer cancel()
	return p.probe(probeCtx, configDir, sc)
}

// duplicatedServers lists the mini servers that Connect must check: those some agent entry duplicates.
func duplicatedServers(duplicates ...map[string]string) []string {
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
