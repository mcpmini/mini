package importers

import (
	"encoding/json"
	"fmt"
)

type geminiMCPEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	HTTPUrl string            `json:"httpUrl"`
	Headers map[string]string `json:"headers"`
}

// ImportFromGemini reads a Gemini CLI settings.json.
// Format: mcpServers map with httpUrl (HTTP) or command/args (stdio).
func ImportFromGemini(configDir, path string) error {
	servers, err := loadGeminiServers(path)
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		fmt.Println("no mcpServers found in Gemini CLI config")
		return nil
	}
	return writeGeminiServers(configDir, servers)
}

func writeGeminiServers(configDir string, servers map[string]geminiMCPEntry) error {
	for name, entry := range servers {
		if err := WriteServerYAML(configDir, name, geminiEntryToServer(name, entry)); err != nil {
			return err
		}
	}
	fmt.Println("tip: replace any literal tokens in headers with ${ENV_VAR} references")
	return nil
}

func loadGeminiServers(path string) (map[string]geminiMCPEntry, error) {
	var cfg struct {
		McpServers map[string]geminiMCPEntry `json:"mcpServers"`
	}
	data, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg.McpServers, nil
}

func geminiEntryToServer(name string, entry geminiMCPEntry) ServerYAML {
	sc := ServerYAML{Name: name}
	if entry.HTTPUrl != "" {
		sc.Transport = "http"
		sc.URL = entry.HTTPUrl
		sc.Headers = entry.Headers
		return sc
	}
	sc.Command = entry.Command
	sc.Args = entry.Args
	sc.Env = envList(entry.Env)
	return sc
}
