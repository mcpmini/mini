package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

type agentClient struct {
	Name       string
	ConfigPath string
	Format     string // "claude" (mcpServers key) or "cursor" (same format, different path)
}

func detectAgentClients() []agentClient {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	candidates := knownClients(home)
	var found []agentClient
	for _, c := range candidates {
		if _, err := os.Stat(c.ConfigPath); err == nil {
			found = append(found, c)
		}
	}
	return found
}

func knownClients(home string) []agentClient {
	clients := []agentClient{
		{Name: "Claude Code", ConfigPath: filepath.Join(home, ".claude.json"), Format: "claude"},
		{Name: "Cursor", ConfigPath: filepath.Join(home, ".cursor", "mcp.json"), Format: "claude"},
		{Name: "Windsurf", ConfigPath: filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), Format: "claude"},
		{Name: "Gemini CLI", ConfigPath: filepath.Join(home, ".gemini", "settings.json"), Format: "claude"},
	}
	clients = append(clients, claudeDesktopPath(home))
	return clients
}

func claudeDesktopPath(home string) agentClient {
	var path string
	switch runtime.GOOS {
	case "darwin":
		path = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			path = filepath.Join(appData, "Claude", "claude_desktop_config.json")
		}
	default:
		path = filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
	return agentClient{Name: "Claude Desktop", ConfigPath: path, Format: "claude"}
}

func miniSnippet(binaryPath string) map[string]any {
	return map[string]any{
		"command": binaryPath,
		"args":    []string{},
	}
}

func renderMinimcpInstallJSON(binaryPath string) string {
	full := map[string]any{
		"mcpServers": map[string]any{
			"mini": miniSnippet(binaryPath),
		},
	}
	b, _ := json.MarshalIndent(full, "", "  ")
	return string(b)
}
