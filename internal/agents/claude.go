package agents

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
)

type claudeMCPEntry struct {
	clientEntryFields
	Type      string `json:"type"`
	URL       string `json:"url"`
	ServerURL string `json:"serverUrl"`
	Disabled  bool   `json:"disabled"`
}

// One format covers Claude Code, Claude Desktop, Cursor and Windsurf; each ignores the others' keys.
var claudeFormat = entryFormat{kinds: map[string]keyKind{
	"type": keyMapped, "command": keyMapped, "args": keyMapped, "env": keyMapped,
	"url": keyMapped, "serverUrl": keyMapped, "headers": keyMapped, "disabled": keyMapped,
	"disabledTools": keyLimitsTools,
}}

// ReadClaude reads Claude Desktop and Claude Code configs, and Cursor's and Windsurf's, which share their format.
func ReadClaude(path string) (map[string]Server, error) {
	data, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := claudeMCPServers(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	entries, keys, err := decodeJSONEntries[claudeMCPEntry](raw)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return importedServers(entries, keys, claudeFormat), nil
}

// claudeMCPServers handles both Claude Desktop (top-level mcpServers)
// and Claude Code (~/.claude.json, projects[path].mcpServers) formats.
func claudeMCPServers(data []byte) (map[string]json.RawMessage, error) {
	var doc struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
		Projects   map[string]struct {
			McpServers map[string]json.RawMessage `json:"mcpServers"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.McpServers) > 0 {
		return doc.McpServers, nil
	}
	merged := map[string]json.RawMessage{}
	for _, project := range slices.Sorted(maps.Keys(doc.Projects)) {
		mergeClaudeProjectServers(merged, doc.Projects[project].McpServers)
	}
	return merged, nil
}

func mergeClaudeProjectServers(dst, src map[string]json.RawMessage) {
	for _, name := range slices.Sorted(maps.Keys(src)) {
		if _, exists := dst[name]; exists {
			fmt.Fprintf(os.Stderr, "warning: duplicate server name %q across projects — keeping first seen\n", name)
			continue
		}
		dst[name] = src[name]
	}
}

func decodeJSONEntries[E any](raw map[string]json.RawMessage) (map[string]E, map[string][]string, error) {
	entries := make(map[string]E, len(raw))
	keys := make(map[string][]string, len(raw))
	for name, message := range raw {
		var fields map[string]json.RawMessage
		var entry E
		if err := json.Unmarshal(message, &fields); err != nil {
			return nil, nil, fmt.Errorf("server %q: %w", name, err)
		}
		if err := json.Unmarshal(message, &entry); err != nil {
			return nil, nil, fmt.Errorf("server %q: %w", name, err)
		}
		entries[name], keys[name] = entry, slices.Collect(maps.Keys(fields))
	}
	return entries, keys, nil
}

func (e claudeMCPEntry) server(name string) Server {
	fields := e.clientEntryFields
	fields.Env, fields.Headers = translateRefs(fields.Env, false), translateRefs(fields.Headers, false)
	url := e.URL
	if url == "" {
		url = e.ServerURL
	}
	if url != "" || e.Type == "http" || e.Type == "sse" {
		return Server{Config: fields.httpServer(name, url), Disabled: e.Disabled}
	}
	return Server{Config: fields.stdioServer(name), Disabled: e.Disabled}
}
