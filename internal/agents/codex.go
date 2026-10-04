package agents

import (
	"fmt"

	"github.com/BurntSushi/toml"

	"github.com/mcpmini/mini/internal/config"
)

type codexMCPEntry struct {
	clientEntryFields
	Transport string `toml:"transport"`
	URL       string `toml:"url"`
}

// ReadCodex reads a Codex config.toml.
// Format: [mcp_servers.NAME] sections with command/args/env or url fields.
func ReadCodex(path string) (map[string]config.ServerConfig, error) {
	entries, err := loadCodexServers(path)
	if err != nil {
		return nil, err
	}
	return serverConfigs(entries), nil
}

func loadCodexServers(path string) (map[string]codexMCPEntry, error) {
	var cfg struct {
		McpServers map[string]codexMCPEntry `toml:"mcp_servers"`
	}
	data, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg.McpServers, nil
}

func (e codexMCPEntry) serverConfig(name string) config.ServerConfig {
	if e.URL != "" || e.Transport == "http" {
		return e.httpServer(name, e.URL)
	}
	return e.stdioServer(name)
}
