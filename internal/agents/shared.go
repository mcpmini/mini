package agents

import (
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
	defer f.Close() //nolint:errcheck
	data, err := io.ReadAll(io.LimitReader(f, maxImportConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImportConfigBytes {
		return nil, fmt.Errorf("%s: too large (over %d MiB)", path, maxImportConfigBytes>>20)
	}
	return data, nil
}

// Server is one agent entry as mini would import it.
type Server struct {
	Config             config.ServerConfig
	Disabled           bool
	IgnoredRunSettings []string
	UnexpandableRefs   []string
	// UnusedEnvHeaders maps a static header kept at import to the variable the agent would read
	// it from once that is set; mini won't switch to it later.
	UnusedEnvHeaders map[string]string
}

func switchedOff(enabled *bool) bool {
	return enabled != nil && !*enabled
}

func (s Server) Candidate() bool {
	return len(s.UnexpandableRefs) == 0
}

type entryFormat struct {
	// Unmapped keys not listed here are dropped without notice, so a setting an agent adds later
	// never blocks an import.
	ignoredRunSettings []string
	expandsBareVars    bool
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
	bareEnvRef   = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)
	miniEnvRef   = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
	bracedRef    = regexp.MustCompile(`\$\{`)
	braceOrBare  = regexp.MustCompile(`\$(\{|[A-Za-z_])`)
)

func translateRefs(values map[string]string, f entryFormat) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		v = cursorEnvRef.ReplaceAllString(v, "$${$1}")
		if f.expandsBareVars {
			v = bareEnvRef.ReplaceAllString(v, "$${$1}")
		}
		out[k] = v
	}
	return out
}

// mini doesn't expand ${VAR} in url, command or args, and reads no other reference syntax, so a
// copy with such a reference wouldn't run the way the agent's entry does.
func unexpandableFields(sc config.ServerConfig, f entryFormat) []string {
	ref := bracedRef
	if f.expandsBareVars {
		ref = braceOrBare
	}
	var fields []string
	if ref.MatchString(sc.URL) {
		fields = append(fields, "an environment variable in url")
	}
	if ref.MatchString(sc.Command) || slices.ContainsFunc(sc.Args, ref.MatchString) {
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

// Cursor and Windsurf substitute these themselves; mini would read them as environment variables.
var editorPlaceholder = regexp.MustCompile(`\$\{(userHome|workspaceFolder|workspaceFolderBasename|pathSeparator)\}`)

func hasForeignRef(value string) bool {
	return strings.Contains(miniEnvRef.ReplaceAllString(value, ""), "${")
}
