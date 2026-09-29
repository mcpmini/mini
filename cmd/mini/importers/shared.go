package importers

import (
	"errors"
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

// AddServerYAML adds one new server, refusing a configured name with
// ops.ErrAlreadyConfigured.
func AddServerYAML(configDir, name string, sc ServerYAML) error {
	saved, err := ops.AddServer(configDir, toServerConfig(name, sc))
	if err != nil {
		return err
	}
	saved.Print(os.Stdout, name)
	return nil
}

// ImportServer adds one server of a batch import. A configured name is skipped
// rather than failing the batch, so re-running an import adds only what's new.
func ImportServer(configDir, name string, sc ServerYAML) (added bool, err error) {
	err = AddServerYAML(configDir, name, sc)
	if errors.Is(err, ops.ErrAlreadyConfigured) {
		fmt.Printf("skipped %s: already configured\n", name)
		return false, nil
	}
	return err == nil, err
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
