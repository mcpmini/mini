package agents

import (
	"fmt"
	"maps"
	"os"
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

var codexFormat = entryFormat{ignoredRunSettings: []string{"cwd"}}

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

func (e codexMCPEntry) headers() map[string]string {
	headers := maps.Clone(e.Headers)
	set := func(name, value string) {
		if headers == nil {
			headers = map[string]string{}
		}
		headers[name] = value
	}
	for name, envVar := range e.EnvHTTPHeaders {
		// Codex leaves the header out while its variable is unset; mini would refuse to start the server.
		if os.Getenv(envVar) != "" {
			set(name, "${"+envVar+"}")
		}
	}
	if e.BearerTokenEnvVar != "" {
		set("Authorization", "Bearer ${"+e.BearerTokenEnvVar+"}")
	}
	return headers
}
