package agents

import (
	"encoding/json"
	"fmt"
)

type geminiMCPEntry struct {
	clientEntryFields
	HTTPUrl string `json:"httpUrl"`
	URL     string `json:"url"`
}

var geminiFormat = entryFormat{bareRefs: true, kinds: map[string]keyKind{
	"command": keyMapped, "args": keyMapped, "env": keyMapped, "headers": keyMapped,
	"url": keyMapped, "httpUrl": keyMapped,
	"timeout": keyDropped, "trust": keyDropped, "description": keyDropped,
	"includeTools": keyLimitsTools, "excludeTools": keyLimitsTools,
}}

// ReadGemini reads a Gemini CLI settings.json.
// Format: mcpServers map with httpUrl (streamable HTTP), url (SSE) or command/args (stdio).
func ReadGemini(path string) (map[string]Server, error) {
	var cfg struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	data, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	entries, keys, err := decodeJSONEntries[geminiMCPEntry](cfg.McpServers)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return importedServers(entries, keys, geminiFormat), nil
}

func (e geminiMCPEntry) server(name string) Server {
	fields := e.clientEntryFields
	fields.Env, fields.Headers = translateRefs(fields.Env, true), translateRefs(fields.Headers, true)
	if e.HTTPUrl != "" {
		return Server{Config: fields.httpServer(name, e.HTTPUrl)}
	}
	if e.URL != "" {
		return Server{Config: fields.httpServer(name, e.URL)}
	}
	return Server{Config: fields.stdioServer(name)}
}
