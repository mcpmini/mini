package agents

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// ReadAnyConfig reads a config file the user named in Codex's, OpenClaw's or the mcpServers format.
func ReadAnyConfig(path string) (map[string]Server, error) {
	if strings.EqualFold(filepath.Ext(path), ".toml") {
		return ReadCodex(path)
	}
	return readParsed(path, parseAnyJSON)
}

func parseAnyJSON(data []byte) (map[string]Server, error) {
	var keys struct {
		MCPServers json.RawMessage `json:"mcpServers"`
		MCP        json.RawMessage `json:"mcp"`
	}
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, err
	}
	if keys.MCPServers == nil && keys.MCP != nil {
		return parseOpenClaw(data)
	}
	return ParseClaude(data)
}
