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

// WriteServerYAML writes servers/<name>.yaml, replacing an existing one.
func WriteServerYAML(configDir, name string, sc ServerYAML) error {
	added, err := ops.WriteServer(configDir, toServerConfig(name, sc))
	if err != nil {
		return err
	}
	PrintAdded(os.Stdout, added)
	return nil
}

func AddServerYAML(configDir, name string, sc ServerYAML) (ops.AddedServer, error) {
	return ops.AddServer(configDir, toServerConfig(name, sc))
}

func PrintAdded(w io.Writer, added ops.AddedServer) {
	fmt.Fprintf(w, "added %s → %s\n", added.Config.Name, added.Path)
	if added.ProjectionPath != "" {
		fmt.Fprintf(w, "installed default projection → %s\n", added.ProjectionPath)
	}
	if added.DefaultPermissions {
		fmt.Fprintf(w, "applied default permissions → %s\n", added.Path)
	}
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
