package importers

import (
	"encoding/json"
	"fmt"
)

type openClawMCPEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// ImportFromOpenClaw reads an OpenClaw (formerly MoltBot) openclaw.json config.
// Format: {"mcp": {"servers": {"name": {"command": "...", "args": [...], "env": {...}}}}}
func ImportFromOpenClaw(configDir, path string) error {
	servers, err := parseOpenClawConfig(path)
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		fmt.Println("no mcp.servers found in OpenClaw config")
		return nil
	}
	return writeOpenClawServers(configDir, servers)
}

func writeOpenClawServers(configDir string, servers map[string]openClawMCPEntry) error {
	for name, entry := range servers {
		if err := WriteServerYAML(configDir, name, openClawEntryToServer(name, entry)); err != nil {
			return err
		}
	}
	fmt.Println("tip: replace any literal tokens in env with ${ENV_VAR} references")
	return nil
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

func openClawEntryToServer(name string, entry openClawMCPEntry) ServerYAML {
	sc := ServerYAML{Name: name}
	if entry.URL != "" {
		sc.Transport = "http"
		sc.URL = entry.URL
		sc.Headers = entry.Headers
		return sc
	}
	sc.Command = entry.Command
	sc.Args = entry.Args
	sc.Env = envList(entry.Env)
	return sc
}
