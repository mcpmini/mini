package ops

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
)

// WriteServer validates name, writes servers/<name>.yaml, and installs a
// bundled projection if the server is a known upstream.
func WriteServer(configDir string, sc config.ServerConfig) error {
	if !config.ValidServerName.MatchString(sc.Name) {
		return fmt.Errorf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", sc.Name)
	}
	if err := writeServerYAML(configDir, sc); err != nil {
		return err
	}
	InstallBundledProjection(configDir, sc)
	installBundledPermissions(configDir, sc)
	return nil
}

func writeServerYAML(configDir string, sc config.ServerConfig) error {
	dir := filepath.Join(configDir, "servers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create servers dir: %w", err)
	}
	path := filepath.Join(dir, sc.Name+".yaml")
	data, _ := yaml.Marshal(sc)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Printf("added %s → %s\n", sc.Name, path)
	return nil
}

// DeleteServer removes servers/<name>.yaml and any oauth-detected marker for it —
// otherwise a later server reusing the same name would inherit stale auth state.
func DeleteServer(configDir, name string) error {
	if !config.ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", name)
	}
	path := filepath.Join(configDir, "servers", name+".yaml")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	os.Remove(config.ServerMetaPath(configDir, name)) //nolint:errcheck
	return nil
}
