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
	var cfg struct {
		McpServers map[string]codexMCPEntry `toml:"mcp_servers"`
	}
	data, err := ReadConfigFile(path)
	if err != nil {
		return err
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.McpServers) == 0 {
		fmt.Println("no mcp_servers found in Codex config")
		return nil
	}
	for name, entry := range cfg.McpServers {
		if err := WriteServerYAML(configDir, name, codexEntryToServer(name, entry)); err != nil {
			return err
		}
	}
	fmt.Println("tip: replace any literal tokens in env with ${ENV_VAR} references")
	return nil
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
