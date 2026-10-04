package agents

import (
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/config"
)

type openClawMCPEntry struct {
	clientEntryFields
	URL string `json:"url"`
}

// ReadOpenClaw reads an OpenClaw (formerly MoltBot) openclaw.json config.
// Format: {"mcp": {"servers": {"name": {"command": "...", "args": [...], "env": {...}}}}}
func ReadOpenClaw(path string) (map[string]config.ServerConfig, error) {
	entries, err := parseOpenClawConfig(path)
	if err != nil {
		return nil, err
	}
	return serverConfigs(entries), nil
}

func parseOpenClawConfig(path string) (map[string]openClawMCPEntry, error) {
	var cfg struct {
		MCP struct {
			Servers map[string]openClawMCPEntry `json:"servers"`
		} `json:"mcp"`
	}
	data, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg.MCP.Servers, nil
}

func (e openClawMCPEntry) serverConfig(name string) config.ServerConfig {
	if e.URL != "" {
		return e.httpServer(name, e.URL)
	}
	return e.stdioServer(name)
}
