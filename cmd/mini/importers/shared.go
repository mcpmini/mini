package importers

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
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
	if !config.ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", name)
	}
	dir := filepath.Join(configDir, "servers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create servers dir: %w", err)
	}
	path := filepath.Join(dir, name+".yaml")
	data, _ := yaml.Marshal(sc)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Printf("added %s → %s\n", name, path)
	InstallBundledProjection(configDir, sc)
	return nil
}

func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
