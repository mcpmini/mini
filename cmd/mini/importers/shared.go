package importers

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

func httpServer(name, url string, headers map[string]string) config.ServerConfig {
	return config.ServerConfig{Name: name, Transport: "http", URL: url, Headers: headers}
}

func stdioServer(name, command string, args []string, env map[string]string) config.ServerConfig {
	return config.ServerConfig{Name: name, Command: command, Args: args, Env: envList(env)}
}

func envList(env map[string]string) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(env)) {
		out = append(out, k+"="+env[k])
	}
	return out
}
