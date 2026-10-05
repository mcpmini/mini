package agents

import (
	"encoding/json"
	"slices"
)

type geminiMCPEntry struct {
	clientEntryFields
	HTTPUrl string `json:"httpUrl"`
	URL     string `json:"url"`
}

var geminiFormat = entryFormat{expandsBareVars: true, ignoredRunSettings: []string{"cwd", "authProviderType", "oauth"}}

// ReadGemini reads a Gemini CLI settings.json.
// Format: mcpServers map with httpUrl (streamable HTTP), url (SSE) or command/args (stdio).
func ReadGemini(path string) (map[string]Server, error) {
	return readParsed(path, ParseGemini)
}

func ParseGemini(data []byte) (map[string]Server, error) {
	var cfg struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
		MCP        geminiServerLists          `json:"mcp"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	entries, keys, err := decodeJSONEntries[geminiMCPEntry](cfg.McpServers)
	if err != nil {
		return nil, err
	}
	servers := importedServers(entries, keys, geminiFormat)
	for name, s := range servers {
		s.Disabled = s.Disabled || cfg.MCP.switchesOff(name)
		servers[name] = s
	}
	return servers, nil
}

// Gemini switches servers off in settings.json's mcp lists rather than in their entries.
type geminiServerLists struct {
	Allowed  []string `json:"allowed"`
	Excluded []string `json:"excluded"`
}

func (l geminiServerLists) switchesOff(name string) bool {
	return slices.Contains(l.Excluded, name) || (len(l.Allowed) > 0 && !slices.Contains(l.Allowed, name))
}

func (e geminiMCPEntry) server(name string) Server {
	fields := e.clientEntryFields
	fields.Env, fields.Headers = translateRefs(fields.Env, geminiFormat), translateRefs(fields.Headers, geminiFormat)
	if e.HTTPUrl != "" {
		return Server{Config: fields.httpServer(name, e.HTTPUrl)}
	}
	if e.URL != "" {
		return Server{Config: fields.httpServer(name, e.URL)}
	}
	return Server{Config: fields.stdioServer(name)}
}
