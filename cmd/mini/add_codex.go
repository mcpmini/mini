package main

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// importFromCodex reads a Codex config.toml.
// Format: [mcp_servers.NAME] sections with command/args/env or url fields.
func importFromCodex(configDir, path string) {
	var cfg struct {
		McpServers map[string]codexMCPEntry `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(string(readConfigFile(path)), &cfg); err != nil {
		fatalf("parse %s: %v", path, err)
	}
	if len(cfg.McpServers) == 0 {
		fmt.Println("no mcp_servers found in Codex config")
		return
	}
	for name, entry := range cfg.McpServers {
		writeServerYAML(configDir, name, codexEntryToServer(name, entry))
	}
	fmt.Println("tip: replace any literal tokens in env with ${ENV_VAR} references")
}

type codexMCPEntry struct {
	Command   string            `toml:"command"`
	Args      []string          `toml:"args"`
	Env       map[string]string `toml:"env"`
	Transport string            `toml:"transport"`
	URL       string            `toml:"url"`
	Headers   map[string]string `toml:"headers"`
}

func codexEntryToServer(name string, entry codexMCPEntry) serverYAML {
	sc := serverYAML{Name: name}
	if entry.URL != "" || entry.Transport == "http" {
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
