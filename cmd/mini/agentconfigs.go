package main

import "encoding/json"

func miniSnippet(binaryPath string) map[string]any {
	return map[string]any{
		"command": binaryPath,
		"args":    []string{"connect"},
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
