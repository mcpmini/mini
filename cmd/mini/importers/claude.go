package importers

import (
	"encoding/json"
	"fmt"
	"os"
)

type ClaudeMCPEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func ImportFromClaude(configDir, path string) error {
	data, err := ReadConfigFile(path)
	if err != nil {
		return err
	}
	servers := ExtractClaudeMCPServers(data)
	return importClaudeServers(configDir, servers, "no mcpServers found in config")
}

// ImportFromCursor reads a Cursor mcp.json, which uses the same mcpServers
// JSON format as Claude Desktop.
func ImportFromCursor(configDir, path string) error {
	data, err := ReadConfigFile(path)
	if err != nil {
		return err
	}
	servers := ExtractClaudeMCPServers(data)
	return importClaudeServers(configDir, servers, "no mcpServers found in Cursor config")
}

func importClaudeServers(configDir string, servers map[string]ClaudeMCPEntry, emptyMsg string) error {
	if len(servers) == 0 {
		fmt.Println(emptyMsg)
		return nil
	}
	for name, entry := range servers {
		if err := WriteServerYAML(configDir, name, ClaudeEntryToServer(name, entry)); err != nil {
			return err
		}
	}
	fmt.Println("tip: replace any literal tokens in headers with ${ENV_VAR} references")
	return nil
}

// ExtractClaudeMCPServers handles both Claude Desktop (top-level mcpServers)
// and Claude Code (~/.claude.json, projects[path].mcpServers) formats.
func ExtractClaudeMCPServers(data []byte) map[string]ClaudeMCPEntry {
	if servers := tryClaudeDesktopFormat(data); len(servers) > 0 {
		return servers
	}
	return tryClaudeCodeFormat(data)
}

func tryClaudeDesktopFormat(data []byte) map[string]ClaudeMCPEntry {
	var desktop struct {
		McpServers map[string]ClaudeMCPEntry `json:"mcpServers"`
	}
	if json.Unmarshal(data, &desktop) == nil {
		return desktop.McpServers
	}
	return nil
}

func tryClaudeCodeFormat(data []byte) map[string]ClaudeMCPEntry {
	var claudeCode struct {
		Projects map[string]struct {
			McpServers map[string]ClaudeMCPEntry `json:"mcpServers"`
		} `json:"projects"`
	}
	if json.Unmarshal(data, &claudeCode) != nil {
		return nil
	}
	merged := map[string]ClaudeMCPEntry{}
	for _, proj := range claudeCode.Projects {
		mergeClaudeProjectServers(merged, proj.McpServers)
	}
	return merged
}

func mergeClaudeProjectServers(dst, src map[string]ClaudeMCPEntry) {
	for name, entry := range src {
		if _, exists := dst[name]; exists {
			fmt.Fprintf(os.Stderr, "warning: duplicate server name %q across projects — keeping first seen\n", name)
			continue
		}
		dst[name] = entry
	}
}

func ClaudeEntryToServer(name string, entry ClaudeMCPEntry) ServerYAML {
	sc := ServerYAML{Name: name}
	if entry.URL != "" || entry.Type == "http" || entry.Type == "sse" {
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
