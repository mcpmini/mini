package importers

import (
	"fmt"
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

const maxImportConfigBytes = 10 << 20

func ReadConfigFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() > maxImportConfigBytes {
		return nil, fmt.Errorf("%s is too large (%d bytes)", path, info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// WriteServerYAML writes servers/<name>.yaml and installs a bundled projection
// if one is known for this server.
func WriteServerYAML(configDir, name string, sc ServerYAML) error {
	return ops.WriteServer(configDir, toServerConfig(name, sc))
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
		cfg.Permissions = &config.PermissionsConfig{
			Protected: sc.Permissions.Protected,
			Hidden:    sc.Permissions.Hidden,
		}
	}
	return cfg
}

func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
