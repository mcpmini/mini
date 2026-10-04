package agents

import (
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/config"
)

type geminiMCPEntry struct {
	clientEntryFields
	HTTPUrl string `json:"httpUrl"`
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
		return e.httpServer(name, e.HTTPUrl)
	}
	return e.stdioServer(name)
}
