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

// Server is one agent entry as mini would import it, with what the agent does that mini can't.
type Server struct {
	Config      config.ServerConfig
	Disabled    bool
	LimitsTools bool
	// Unsupported names the settings mini can't carry over; a copy in mini would run differently.
	Unsupported []string
}

// Candidate reports whether a copy in mini would behave like the agent's entry and expose
// no tools the user forbade there.
func (s Server) Candidate() bool {
	return !s.LimitsTools && len(s.Unsupported) == 0
}

type keyKind int

const (
	keyMapped keyKind = iota
	keyDropped
	keyLimitsTools
)

// entryFormat is what an agent's config format means beyond the fields a reader maps.
type entryFormat struct {
	kinds map[string]keyKind
	// bareRefs is set for agents that expand $VAR as well as ${VAR}.
	bareRefs bool
}

type clientEntry interface {
	server(name string) Server
}

func importedServers[E clientEntry](entries map[string]E, keys map[string][]string, f entryFormat) map[string]Server {
	servers := make(map[string]Server, len(entries))
	for name, entry := range entries {
		s := entry.server(name)
		limits, unsupported := classifyKeys(keys[name], f.kinds)
		s.LimitsTools = s.LimitsTools || limits
		s.Unsupported = append(unsupported, unexpandableFields(s.Config, f.bareRefs)...)
		servers[name] = s
	}
	return servers
}

func classifyKeys(keys []string, kinds map[string]keyKind) (bool, []string) {
	limitsTools := false
	var unsupported []string
	for _, key := range slices.Sorted(slices.Values(keys)) {
		kind, known := kinds[key]
		switch {
		case !known:
			unsupported = append(unsupported, key)
		case kind == keyLimitsTools:
			limitsTools = true
		}
	}
	return limitsTools, unsupported
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

// translateRefs rewrites the agent's environment references into mini's ${VAR}, so secrets stay
// references and are never copied into mini's files.
func translateRefs(values map[string]string, bareRefs bool) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		v = cursorEnvRef.ReplaceAllString(v, "$${$1}")
		if bareRefs {
			v = bareEnvRef.ReplaceAllString(v, "$${$1}")
		}
		out[k] = v
	}
	return out
}

// mini expands ${VAR} only in env and header values; a reference anywhere else, or in another
// syntax, would reach the server unexpanded.
func unexpandableFields(sc config.ServerConfig, bareRefs bool) []string {
	ref := bracedRef
	if bareRefs {
		ref = braceOrBare
	}
	var fields []string
	if ref.MatchString(sc.URL) {
		fields = append(fields, "an environment variable in url")
	}
	if ref.MatchString(sc.Command) || slices.ContainsFunc(sc.Args, ref.MatchString) {
		fields = append(fields, "an environment variable in command or args")
	}
	if slices.ContainsFunc(sc.Env, hasForeignRef) || slices.ContainsFunc(slices.Collect(maps.Values(sc.Headers)), hasForeignRef) {
		fields = append(fields, "an environment variable syntax mini doesn't read")
	}
	return fields
}

func hasForeignRef(value string) bool {
	return strings.Contains(miniEnvRef.ReplaceAllString(value, ""), "${")
}
