package ops

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/defaults"
)

// WriteServer validates name, writes servers/<name>.yaml, and installs a
// bundled projection if the server is a known upstream.
func WriteServer(configDir string, sc config.ServerConfig) error {
	if !config.ValidServerName.MatchString(sc.Name) {
		return fmt.Errorf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", sc.Name)
	}
	if err := applyBundledAuth(&sc); err != nil {
		return err
	}
	if err := writeServerYAML(configDir, sc); err != nil {
		return err
	}
	InstallBundledProjection(configDir, sc)
	installBundledPermissions(configDir, sc)
	return nil
}

func applyBundledAuth(sc *config.ServerConfig) error {
	if sc.Auth != nil {
		return nil
	}
	data := defaults.AuthFor(sc.Name)
	if data == nil {
		return nil
	}
	var ac config.AuthConfig
	if err := yaml.Unmarshal(data, &ac); err != nil {
		return fmt.Errorf("parse bundled auth for %s: %w", sc.Name, err)
	}
	sc.Auth = &ac
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

func PersistAuthConfig(configDir, serverName string, ac config.AuthConfig) error {
	if !config.ValidServerName.MatchString(serverName) {
		return fmt.Errorf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", serverName)
	}
	path := filepath.Join(configDir, "servers", serverName+".yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// No config file to persist to (e.g. an in-memory runtime-added server) — nothing to do.
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var sc config.ServerConfig
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	sc.Auth = &ac
	out, err := yaml.Marshal(sc)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	return os.WriteFile(path, out, 0600)
}

// DeleteServer removes servers/<name>.yaml.
func DeleteServer(configDir, name string) error {
	if !config.ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", name)
	}
	path := filepath.Join(configDir, "servers", name+".yaml")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
