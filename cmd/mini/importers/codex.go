package importers

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

type codexMCPEntry struct {
	Command   string            `toml:"command"`
	Args      []string          `toml:"args"`
	Env       map[string]string `toml:"env"`
	Transport string            `toml:"transport"`
	URL       string            `toml:"url"`
	Headers   map[string]string `toml:"headers"`
}

// ImportFromCodex reads a Codex config.toml.
// Format: [mcp_servers.NAME] sections with command/args/env or url fields.
func ImportFromCodex(configDir, path string) error {
	servers, err := loadCodexServers(path)
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		fmt.Println("no mcp_servers found in Codex config")
		return nil
	}
	return writeCodexServers(configDir, servers)
}

func writeCodexServers(configDir string, servers map[string]codexMCPEntry) error {
	for name, entry := range servers {
		if err := WriteServerYAML(configDir, name, codexEntryToServer(name, entry)); err != nil {
			return err
		}
	}
	fmt.Println("tip: replace any literal tokens in env with ${ENV_VAR} references")
	return nil
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

func codexEntryToServer(name string, entry codexMCPEntry) ServerYAML {
	sc := ServerYAML{Name: name}
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
