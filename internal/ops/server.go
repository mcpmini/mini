package ops

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
)

var (
	ErrAlreadyConfigured = errors.New("already configured")
	ErrDefinedInline     = errors.New("defined in config.yaml")
)

// SavedServer lists the files written for a server, so the CLI can report them;
// ops never prints, since the config tool runs where stdout is the MCP stream.
type SavedServer struct {
	Path               string
	ProjectionPath     string
	DefaultPermissions bool
}

func (s SavedServer) Print(w io.Writer, name string) {
	fmt.Fprintf(w, "added %s → %s\n", name, s.Path)
	if s.ProjectionPath != "" {
		fmt.Fprintf(w, "installed default projection → %s\n", s.ProjectionPath)
	}
	if s.DefaultPermissions {
		fmt.Fprintf(w, "applied default permissions → %s\n", s.Path)
	}
}

// WriteServer validates name, writes servers/<name>.yaml, and installs a
// bundled projection if the server is a known upstream.
func WriteServer(configDir string, sc config.ServerConfig) error {
	saved, err := writeServer(configDir, sc)
	if err != nil {
		return err
	}
	saved.Print(os.Stdout, sc.Name)
	return nil
}

// AddServer writes a new server. It refuses a configured name so replacing a
// working server always takes an explicit remove first.
func AddServer(configDir string, sc config.ServerConfig) (SavedServer, error) {
	if err := validServerName(sc.Name); err != nil {
		return SavedServer{}, err
	}
	if IsConfigured(configDir, sc.Name) {
		return SavedServer{}, fmt.Errorf("%s is %w", sc.Name, ErrAlreadyConfigured)
	}
	return writeServer(configDir, sc)
}

func IsConfigured(configDir, name string) bool {
	if _, err := os.Stat(serverPath(configDir, name)); err == nil {
		return true
	}
	_, ok := config.LoadServerSet(configDir).Servers[name]
	return ok
}

func writeServer(configDir string, sc config.ServerConfig) (SavedServer, error) {
	if err := validServerName(sc.Name); err != nil {
		return SavedServer{}, err
	}
	withDefaults := WithBundledPermissions(sc)
	path, err := writeServerYAML(configDir, withDefaults)
	if err != nil {
		return SavedServer{}, err
	}
	return SavedServer{
		Path:               path,
		ProjectionPath:     InstallBundledProjection(configDir, sc),
		DefaultPermissions: sc.Permissions == nil && withDefaults.Permissions != nil,
	}, nil
}

func writeServerYAML(configDir string, sc config.ServerConfig) (string, error) {
	path := serverPath(configDir, sc.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("create servers dir: %w", err)
	}
	data, _ := yaml.Marshal(sc)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// DeleteServer removes servers/<name>.yaml and any oauth-detected marker for it —
// otherwise a later server reusing the same name would inherit stale auth state.
func DeleteServer(configDir, name string) error {
	if err := validServerName(name); err != nil {
		return err
	}
	path := serverPath(configDir, name)
	if err := os.Remove(path); err != nil {
		return deleteError(configDir, name, err)
	}
	os.Remove(config.ServerMetaPath(configDir, name)) //nolint:errcheck
	return nil
}

func deleteError(configDir, name string, err error) error {
	if errors.Is(err, fs.ErrNotExist) && IsConfigured(configDir, name) {
		return fmt.Errorf("%s is %w; remove it there", name, ErrDefinedInline)
	}
	return fmt.Errorf("remove %s: %w", serverPath(configDir, name), err)
}

func serverPath(configDir, name string) string {
	return filepath.Join(configDir, "servers", name+".yaml")
}

func validServerName(name string) error {
	if !config.ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", name)
	}
	return nil
}
