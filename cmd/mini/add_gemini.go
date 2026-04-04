package main

import (
	"encoding/json"
	"fmt"
)

// importFromGemini reads a Gemini CLI settings.json.
// Format: mcpServers map with httpUrl (HTTP) or command/args (stdio).
func importFromGemini(configDir, path string) {
	var cfg struct {
		McpServers map[string]geminiMCPEntry `json:"mcpServers"`
	}
	if err := json.Unmarshal(readConfigFile(path), &cfg); err != nil {
		fatalf("parse %s: %v", path, err)
	}
	if len(cfg.McpServers) == 0 {
		fmt.Println("no mcpServers found in Gemini CLI config")
		return
	}
	for name, entry := range cfg.McpServers {
		writeServerYAML(configDir, name, geminiEntryToServer(name, entry))
	}
	fmt.Println("tip: replace any literal tokens in headers with ${ENV_VAR} references")
}

type geminiMCPEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	HTTPUrl string            `json:"httpUrl"`
	Headers map[string]string `json:"headers"`
}

func geminiEntryToServer(name string, entry geminiMCPEntry) serverYAML {
	sc := serverYAML{Name: name}
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
