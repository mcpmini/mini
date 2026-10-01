package importers

import (
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/config"
)

type openClawMCPEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
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
		return httpServer(name, e.URL, e.Headers)
	}
	return stdioServer(name, e.Command, e.Args, e.Env)
}
