package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type claudeMCPEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func importFromClaude(configDir, path string) {
	servers := extractClaudeMCPServers(readConfigFile(path))
	importClaudeServers(configDir, servers, "no mcpServers found in config")
}

// importFromCursor reads a Cursor mcp.json which uses the same mcpServers
// JSON format as Claude Desktop.
func importFromCursor(configDir, path string) {
	servers := extractClaudeMCPServers(readConfigFile(path))
	importClaudeServers(configDir, servers, "no mcpServers found in Cursor config")
}

func importClaudeServers(configDir string, servers map[string]claudeMCPEntry, emptyMsg string) {
	if len(servers) == 0 {
		fmt.Println(emptyMsg)
		return
	}
	for name, entry := range servers {
		writeServerYAML(configDir, name, claudeEntryToServer(name, entry))
	}
	fmt.Println("tip: replace any literal tokens in headers with ${ENV_VAR} references")
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

func claudeEntryToServer(name string, entry claudeMCPEntry) serverYAML {
	sc := serverYAML{Name: name}
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
