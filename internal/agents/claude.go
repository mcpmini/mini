package agents

import (
	"encoding/json"
)

type claudeMCPEntry struct {
	clientEntryFields
	Type      string `json:"type"`
	URL       string `json:"url"`
	ServerURL string `json:"serverUrl"`
	Disabled  bool   `json:"disabled"`
}

var claudeFormat = entryFormat{ignoredRunSettings: []string{"envFile", "headersHelper", "oauth"}, editorPlaceholders: true}

// ReadClaude reads Claude Desktop and Claude Code configs, and Cursor's and Windsurf's, which share their format.
func ReadClaude(path string) (map[string]Server, error) {
	return readParsed(path, ParseClaude)
}

func ParseClaude(data []byte) (map[string]Server, error) {
	raw, err := claudeMCPServers(data)
	if err != nil {
		return nil, err
	}
	entries, keys, err := decodeJSONEntries[claudeMCPEntry](raw)
	if err != nil {
		return nil, err
	}
	return importedServers(entries, keys, claudeFormat), nil
}

// Only user-scoped servers: Claude Code's per-project servers under projects[path] belong to
// those projects and are left alone.
func claudeMCPServers(data []byte) (map[string]json.RawMessage, error) {
	var doc struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.McpServers, nil
}

func (e claudeMCPEntry) server(name string) Server {
	fields := e.clientEntryFields
	fields.Env, fields.Headers = translateRefs(fields.Env, claudeFormat), translateRefs(fields.Headers, claudeFormat)
	url := e.URL
	if url == "" {
		url = e.ServerURL
	}
	if url != "" || e.Type == "http" || e.Type == "sse" {
		return Server{Config: fields.httpServer(name, url), Disabled: e.Disabled}
	}
	return Server{Config: fields.stdioServer(name), Disabled: e.Disabled}
}
