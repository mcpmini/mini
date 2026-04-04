package main

import (
	"encoding/json"
	"fmt"
)

// importFromOpenClaw reads an OpenClaw (formerly MoltBot) openclaw.json config.
// Format: {"mcp": {"servers": {"name": {"command": "...", "args": [...], "env": {...}}}}}
func importFromOpenClaw(configDir, path string) {
	var cfg struct {
		MCP struct {
			Servers map[string]openClawMCPEntry `json:"servers"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(readConfigFile(path), &cfg); err != nil {
		fatalf("parse %s: %v", path, err)
	}
	if len(cfg.MCP.Servers) == 0 {
		fmt.Println("no mcp.servers found in OpenClaw config")
		return
	}
	for name, entry := range cfg.MCP.Servers {
		writeServerYAML(configDir, name, openClawEntryToServer(name, entry))
	}
	fmt.Println("tip: replace any literal tokens in env with ${ENV_VAR} references")
}

type openClawMCPEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func openClawEntryToServer(name string, entry openClawMCPEntry) serverYAML {
	sc := serverYAML{Name: name}
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
