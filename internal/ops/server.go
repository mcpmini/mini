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
	return installServer(configDir, sc, os.O_TRUNC)
}

// CreateServer is WriteServer that never replaces an existing
// servers/<name>.yaml; that case returns an error wrapping fs.ErrExist and
// installs nothing.
func CreateServer(configDir string, sc config.ServerConfig) error {
	if err := installServer(configDir, sc, os.O_EXCL); err != nil {
		return err
	}
	// The server file didn't exist, so a meta file is left over from an earlier server of
	// this name (its YAML deleted by hand) and must not hand it that server's detected OAuth.
	os.Remove(config.ServerMetaPath(configDir, sc.Name)) //nolint:errcheck
	return nil
}

func installServer(configDir string, sc config.ServerConfig, openFlag int) error {
	if !config.ValidServerName.MatchString(sc.Name) {
		return fmt.Errorf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", sc.Name)
	}
	if err := writeServerYAML(configDir, sc, openFlag); err != nil {
		return err
	}
	InstallBundledProjection(configDir, sc)
	installBundledPermissions(configDir, sc)
	return nil
}

func writeServerYAML(configDir string, sc config.ServerConfig, openFlag int) error {
	dir := filepath.Join(configDir, "servers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create servers dir: %w", err)
	}
	path := filepath.Join(dir, sc.Name+".yaml")
	data, _ := yaml.Marshal(sc)
	if err := writeNewOrTruncate(path, data, openFlag); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Printf("added %s → %s\n", sc.Name, path)
	return nil
}

func writeNewOrTruncate(path string, data []byte, openFlag int) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|openFlag, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	// A partly written file this call created would read as an existing server on every rerun.
	if err != nil && openFlag == os.O_EXCL {
		os.Remove(path) //nolint:errcheck
	}
	return err
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
