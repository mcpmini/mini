package agents

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"

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

type clientEntry interface {
	serverConfig(name string) config.ServerConfig
}

func serverConfigs[E clientEntry](entries map[string]E) map[string]config.ServerConfig {
	servers := make(map[string]config.ServerConfig, len(entries))
	for name, entry := range entries {
		servers[name] = entry.serverConfig(name)
	}
	return servers
}

type clientEntryFields struct {
	Command string            `json:"command" toml:"command"`
	Args    []string          `json:"args" toml:"args"`
	Env     map[string]string `json:"env" toml:"env"`
	Headers map[string]string `json:"headers" toml:"headers"`
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
