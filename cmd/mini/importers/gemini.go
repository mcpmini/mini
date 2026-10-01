package importers

import (
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/config"
)

type geminiMCPEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	HTTPUrl string            `json:"httpUrl"`
	Headers map[string]string `json:"headers"`
}

// ReadGemini reads a Gemini CLI settings.json.
// Format: mcpServers map with httpUrl (HTTP) or command/args (stdio).
func ReadGemini(path string) (map[string]config.ServerConfig, error) {
	entries, err := loadGeminiServers(path)
	if err != nil {
		return nil, err
	}
	return serverConfigs(entries), nil
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

func (e geminiMCPEntry) serverConfig(name string) config.ServerConfig {
	if e.HTTPUrl != "" {
		return httpServer(name, e.HTTPUrl, e.Headers)
	}
	return stdioServer(name, e.Command, e.Args, e.Env)
}
