package agents

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/mcpmini/mini/internal/config"
)

// A sanity cap, not an expected size: past it the file is almost certainly not an agent config.
const maxImportConfigBytes = 64 << 20

// Reads whatever path it is given, including a pipe (--from <(...)), like other CLIs do.
// os errors already name the path, so they are returned unwrapped.
func ReadConfigFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // ReadAll reports source read failures; Close only releases the imported config file
	data, err := io.ReadAll(io.LimitReader(f, maxImportConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImportConfigBytes {
		return nil, fmt.Errorf("%s: too large (over %d MiB)", path, maxImportConfigBytes>>20)
	}
	return data, nil
}

func readParsed(path string, parse func(data []byte) (map[string]Server, error)) (map[string]Server, error) {
	data, err := ReadConfigFile(path)
	if err != nil {
		return nil, err
	}
	servers, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return servers, nil
}

// Server is one agent entry as mini would import it.
type Server struct {
	Config             config.ServerConfig
	Disabled           bool
	IgnoredRunSettings []string
	UnexpandableRefs   []string
	// header → the variable Codex would read it from once set; mini keeps the static value.
	UnusedEnvHeaders map[string]string
}

func switchedOff(enabled *bool) bool {
	return enabled != nil && !*enabled
}

func (s Server) Candidate() bool {
	return len(s.UnexpandableRefs) == 0
}

type entryFormat struct {
	// Other unmapped keys are dropped silently, so a setting an agent adds later never blocks an import.
	ignoredRunSettings []string
	editorPlaceholders bool
}

type clientEntry interface {
	server(name string) Server
}

func importedServers[E clientEntry](entries map[string]E, keys map[string][]string, f entryFormat) map[string]Server {
	servers := make(map[string]Server, len(entries))
	for name, entry := range entries {
		s := entry.server(name)
		s.IgnoredRunSettings = presentKeys(keys[name], f.ignoredRunSettings)
		s.UnexpandableRefs = unexpandableFields(s.Config, f)
		servers[name] = s
	}
	return servers
}

func presentKeys(keys, wanted []string) []string {
	var present []string
	for _, key := range wanted {
		if slices.Contains(keys, key) {
			present = append(present, key)
		}
	}
	return present
}

type clientEntryFields struct {
	Command string            `json:"command" toml:"command"`
	Args    []string          `json:"args" toml:"args"`
	Env     map[string]string `json:"env" toml:"env"`
	Headers map[string]string `json:"headers" toml:"http_headers"`
}

func (f clientEntryFields) httpServer(name, url string) config.ServerConfig {
	return config.ServerConfig{Name: name, Transport: "http", URL: url, Headers: f.Headers}
}

func (f clientEntryFields) stdioServer(name string) config.ServerConfig {
	return config.ServerConfig{Name: name, Command: f.Command, Args: f.Args, Env: envList(f.Env)}
}

func envList(env map[string]string) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(env)) {
		out = append(out, k+"="+env[k])
	}
	return out
}

var (
	cursorEnvRef = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)
	miniEnvRef   = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
	bracedRef    = regexp.MustCompile(`\$\{`)
)

func translateRefs(values map[string]string, f entryFormat) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = cursorEnvRef.ReplaceAllString(v, "$${$1}")
	}
	return out
}

func unexpandableFields(sc config.ServerConfig, f entryFormat) []string {
	var fields []string
	if bracedRef.MatchString(sc.URL) {
		fields = append(fields, "an environment variable in url")
	}
	if bracedRef.MatchString(sc.Command) || slices.ContainsFunc(sc.Args, bracedRef.MatchString) {
		fields = append(fields, "an environment variable in command or args")
	}
	values := append(slices.Clone(sc.Env), slices.Collect(maps.Values(sc.Headers))...)
	if f.editorPlaceholders && slices.ContainsFunc(values, editorPlaceholder.MatchString) {
		fields = append(fields, "an editor placeholder like ${userHome}")
	} else if slices.ContainsFunc(values, hasForeignRef) {
		fields = append(fields, "an environment variable syntax mini doesn't read")
	}
	return fields
}

var editorPlaceholder = regexp.MustCompile(`\$\{(userHome|workspaceFolder|workspaceFolderBasename|pathSeparator)\}`)

func hasForeignRef(value string) bool {
	return strings.Contains(miniEnvRef.ReplaceAllString(value, ""), "${")
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
