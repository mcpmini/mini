package importers

import (
	"fmt"
	"io"
	"os"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/ops"
)

type ServerYAML struct {
	Name        string            `yaml:"name"`
	Transport   string            `yaml:"transport,omitempty"`
	URL         string            `yaml:"url,omitempty"`
	Command     string            `yaml:"command,omitempty"`
	Args        []string          `yaml:"args,omitempty"`
	Env         []string          `yaml:"env,omitempty"`
	Headers     map[string]string `yaml:"headers,omitempty"`
	Permissions *PermissionsYAML  `yaml:"permissions,omitempty"`
}

type PermissionsYAML struct {
	Protected []string `yaml:"protected,omitempty"`
	Hidden    []string `yaml:"hidden,omitempty"`
}

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

// WriteServerYAML writes servers/<name>.yaml and installs a bundled projection
// if one is known for this server.
func WriteServerYAML(configDir, name string, sc ServerYAML) error {
	return ops.WriteServer(configDir, toServerConfig(name, sc))
}

// CreateServerYAML is WriteServerYAML that leaves an existing server file
// untouched and returns an error wrapping fs.ErrExist instead.
func CreateServerYAML(configDir, name string, sc ServerYAML) error {
	return ops.CreateServer(configDir, toServerConfig(name, sc))
}

// InstallBundledProjection installs a projection for a known server if one exists.
func InstallBundledProjection(configDir string, sc ServerYAML) {
	ops.InstallBundledProjection(configDir, toServerConfig(sc.Name, sc))
}

func toServerConfig(name string, sc ServerYAML) config.ServerConfig {
	cfg := config.ServerConfig{
		Name:      name,
		Transport: sc.Transport,
		URL:       sc.URL,
		Command:   sc.Command,
		Args:      sc.Args,
		Env:       sc.Env,
		Headers:   sc.Headers,
	}
	if sc.Permissions != nil {
		cfg.Permissions = toPermissionsConfig(sc.Permissions)
	}
	return cfg
}

func toPermissionsConfig(p *PermissionsYAML) *config.PermissionsConfig {
	return &config.PermissionsConfig{Protected: p.Protected, Hidden: p.Hidden}
}

func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
