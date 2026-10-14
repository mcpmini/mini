package agents

import (
	"encoding/json"
	"errors"
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
	switch {
	case keys.MCPServers != nil:
		return ParseClaude(data)
	case hasOpenClawServers(keys.MCP):
		return parseOpenClaw(data)
	}
	return nil, errors.New("no mcpServers key (Claude, Cursor, Windsurf) or mcp.servers key (OpenClaw); " +
		"a .toml file is read as Codex's config")
}

func hasOpenClawServers(mcp json.RawMessage) bool {
	var section struct {
		Servers json.RawMessage `json:"servers"`
	}
	return json.Unmarshal(mcp, &section) == nil && section.Servers != nil
}
