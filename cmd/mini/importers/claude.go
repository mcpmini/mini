package importers

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mcpmini/mini/internal/config"
)

type claudeMCPEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// ReadClaude reads Claude Desktop and Claude Code configs, and Cursor's mcp.json, which shares their format.
func ReadClaude(path string) (map[string]config.ServerConfig, error) {
	data, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	return serverConfigs(extractClaudeMCPServers(data)), nil
}

// extractClaudeMCPServers handles both Claude Desktop (top-level mcpServers)
// and Claude Code (~/.claude.json, projects[path].mcpServers) formats.
func extractClaudeMCPServers(data []byte) map[string]claudeMCPEntry {
	if servers := tryClaudeDesktopFormat(data); len(servers) > 0 {
		return servers
	}
	return tryClaudeCodeFormat(data)
}

func tryClaudeDesktopFormat(data []byte) map[string]claudeMCPEntry {
	var desktop struct {
		McpServers map[string]claudeMCPEntry `json:"mcpServers"`
	}
	if json.Unmarshal(data, &desktop) == nil {
		return desktop.McpServers
	}
	return nil
}

func tryClaudeCodeFormat(data []byte) map[string]claudeMCPEntry {
	var claudeCode struct {
		Projects map[string]struct {
			McpServers map[string]claudeMCPEntry `json:"mcpServers"`
		} `json:"projects"`
	}
	if json.Unmarshal(data, &claudeCode) != nil {
		return nil
	}
	merged := map[string]claudeMCPEntry{}
	for _, proj := range claudeCode.Projects {
		mergeClaudeProjectServers(merged, proj.McpServers)
	}
	return merged
}

func mergeClaudeProjectServers(dst, src map[string]claudeMCPEntry) {
	for name, entry := range src {
		if _, exists := dst[name]; exists {
			fmt.Fprintf(os.Stderr, "warning: duplicate server name %q across projects — keeping first seen\n", name)
			continue
		}
		dst[name] = entry
	}
}

func (e claudeMCPEntry) serverConfig(name string) config.ServerConfig {
	if e.URL != "" || e.Type == "http" || e.Type == "sse" {
		return httpServer(name, e.URL, e.Headers)
	}
	return stdioServer(name, e.Command, e.Args, e.Env)
}
