package agents

import (
	"encoding/json"
	"fmt"
)

type openClawMCPEntry struct {
	clientEntryFields
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
}

var openClawFormat = entryFormat{ignoredRunSettings: []string{"cwd", "sslVerify", "clientCert", "clientKey"}}

// ReadOpenClaw reads an OpenClaw (formerly MoltBot) openclaw.json config.
// Format: {"mcp": {"servers": {"name": {"command": "...", "args": [...], "env": {...}}}}}
func ReadOpenClaw(path string) (map[string]Server, error) {
	var cfg struct {
		MCP struct {
			Servers map[string]json.RawMessage `json:"servers"`
		} `json:"mcp"`
	}
	data, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	entries, keys, err := decodeJSONEntries[openClawMCPEntry](cfg.MCP.Servers)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return importedServers(entries, keys, openClawFormat), nil
}

func (e openClawMCPEntry) server(name string) Server {
	disabled := switchedOff(e.Enabled)
	if e.URL != "" {
		return Server{Config: e.httpServer(name, e.URL), Disabled: disabled}
	}
	return Server{Config: e.stdioServer(name), Disabled: disabled}
}
