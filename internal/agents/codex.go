package agents

import (
	"fmt"
	"maps"
	"slices"

	"github.com/BurntSushi/toml"
)

type codexMCPEntry struct {
	clientEntryFields
	URL               string            `toml:"url"`
	EnvHTTPHeaders    map[string]string `toml:"env_http_headers"`
	BearerTokenEnvVar string            `toml:"bearer_token_env_var"`
	Enabled           *bool             `toml:"enabled"`
}

// Timeouts and the other client-side knobs don't change what the server does, so they are
// dropped; approval and tool filters guard what the user allowed, so they keep the entry in Codex.
var codexFormat = entryFormat{kinds: map[string]keyKind{
	"command": keyMapped, "args": keyMapped, "env": keyMapped, "url": keyMapped,
	"http_headers": keyMapped, "env_http_headers": keyMapped, "bearer_token_env_var": keyMapped, "enabled": keyMapped,
	"startup_timeout_sec": keyDropped, "startup_timeout_ms": keyDropped, "tool_timeout_sec": keyDropped,
	"required": keyDropped, "startup_readiness": keyDropped, "supports_parallel_tool_calls": keyDropped,
	"tool_input_schema_max_bytes": keyDropped, "name": keyDropped,
	"enabled_tools": keyLimitsTools, "disabled_tools": keyLimitsTools, "tools": keyLimitsTools,
	"default_tools_approval_mode": keyLimitsTools, "omit_tools_from": keyLimitsTools,
}}

// ReadCodex reads a Codex config.toml: [mcp_servers.NAME] tables with command/args/env or url.
func ReadCodex(path string) (map[string]Server, error) {
	data, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	entries, keys, err := decodeCodexEntries(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return importedServers(entries, keys, codexFormat), nil
}

func decodeCodexEntries(data []byte) (map[string]codexMCPEntry, map[string][]string, error) {
	var cfg struct {
		McpServers map[string]toml.Primitive `toml:"mcp_servers"`
	}
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, nil, err
	}
	entries := make(map[string]codexMCPEntry, len(cfg.McpServers))
	keys := make(map[string][]string, len(cfg.McpServers))
	for name, primitive := range cfg.McpServers {
		var fields map[string]any
		var entry codexMCPEntry
		if err := md.PrimitiveDecode(primitive, &fields); err != nil {
			return nil, nil, fmt.Errorf("server %q: %w", name, err)
		}
		if err := md.PrimitiveDecode(primitive, &entry); err != nil {
			return nil, nil, fmt.Errorf("server %q: %w", name, err)
		}
		entries[name], keys[name] = entry, slices.Collect(maps.Keys(fields))
	}
	return entries, keys, nil
}

func (e codexMCPEntry) server(name string) Server {
	disabled := e.Enabled != nil && !*e.Enabled
	if e.URL == "" {
		return Server{Config: e.stdioServer(name), Disabled: disabled}
	}
	fields := e.clientEntryFields
	fields.Headers = e.headers()
	return Server{Config: fields.httpServer(name, e.URL), Disabled: disabled}
}

// Codex reads these header values from its environment; mini's ${VAR} keeps them references.
func (e codexMCPEntry) headers() map[string]string {
	headers := maps.Clone(e.Headers)
	set := func(name, value string) {
		if headers == nil {
			headers = map[string]string{}
		}
		headers[name] = value
	}
	for name, envVar := range e.EnvHTTPHeaders {
		set(name, "${"+envVar+"}")
	}
	if e.BearerTokenEnvVar != "" {
		set("Authorization", "Bearer ${"+e.BearerTokenEnvVar+"}")
	}
	return headers
}
