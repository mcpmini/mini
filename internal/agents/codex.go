package agents

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

type codexMCPEntry struct {
	clientEntryFields
	URL               string            `toml:"url"`
	EnvHTTPHeaders    map[string]string `toml:"env_http_headers"`
	BearerTokenEnvVar string            `toml:"bearer_token_env_var"`
	Enabled           *bool             `toml:"enabled"`
}

var codexFormat = entryFormat{ignoredRunSettings: []string{"cwd", "environment_id", "http_headers_helper", "auth", "oauth", "oauth_resource", "scopes"}}

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
	disabled := switchedOff(e.Enabled)
	if e.URL == "" {
		return Server{Config: e.stdioServer(name), Disabled: disabled}
	}
	fields := e.clientEntryFields
	var unusedOverrides map[string]string
	fields.Headers, unusedOverrides = e.headers()
	return Server{Config: fields.httpServer(name, e.URL), Disabled: disabled, UnusedEnvHeaders: unusedOverrides}
}

// mini can't make a header reference optional, so Codex's choice is made once, at import: the
// env override when its variable is set, otherwise the static header, otherwise the reference.
func (e codexMCPEntry) headers() (map[string]string, map[string]string) {
	headers := maps.Clone(e.Headers)
	var unusedOverrides map[string]string
	for _, name := range slices.Sorted(maps.Keys(e.EnvHTTPHeaders)) {
		envVar := e.EnvHTTPHeaders[name]
		switch {
		case strings.TrimSpace(os.Getenv(envVar)) != "" || !hasHeader(headers, name):
			headers = replaceHeader(headers, name, "${"+envVar+"}")
		case unusedOverrides == nil:
			unusedOverrides = map[string]string{name: envVar}
		default:
			unusedOverrides[name] = envVar
		}
	}
	if e.BearerTokenEnvVar != "" {
		headers = replaceHeader(headers, "Authorization", "Bearer ${"+e.BearerTokenEnvVar+"}")
	}
	return headers, unusedOverrides
}

func hasHeader(headers map[string]string, name string) bool {
	return slices.ContainsFunc(slices.Collect(maps.Keys(headers)), func(existing string) bool {
		return strings.EqualFold(existing, name)
	})
}

// Header names ignore case, so a header replaces any spelling of its name.
func replaceHeader(headers map[string]string, name, value string) map[string]string {
	if headers == nil {
		headers = map[string]string{}
	}
	for existing := range headers {
		if strings.EqualFold(existing, name) {
			delete(headers, existing)
		}
	}
	headers[name] = value
	return headers
}
