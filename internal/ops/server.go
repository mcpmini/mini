package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

var ErrAlreadyConfigured = errors.New("already configured")

type AddedServer struct {
	Config             config.ServerConfig
	Path               string
	ProjectionPath     string
	DefaultPermissions bool
}

// AddServer never replaces a server file: a configured name returns ErrAlreadyConfigured.
func AddServer(configDir string, sc config.ServerConfig) (AddedServer, error) {
	if err := validServerName(sc.Name); err != nil {
		return AddedServer{}, err
	}
	added, err := writeServer(configDir, sc, os.O_EXCL)
	if errors.Is(err, fs.ErrExist) {
		err = fmt.Errorf("%s is %w", sc.Name, ErrAlreadyConfigured)
	}
	if err != nil {
		return AddedServer{}, err
	}
	if err := forgetStateStoredByName(configDir, sc.Name); err != nil {
		os.Remove(added.Path) //nolint:errcheck
		return AddedServer{}, err
	}
	added.ProjectionPath = InstallBundledProjection(configDir, sc)
	return added, nil
}

// WriteServer replaces any existing server file and keeps the name's stored state.
func WriteServer(configDir string, sc config.ServerConfig) (AddedServer, error) {
	if err := validServerName(sc.Name); err != nil {
		return AddedServer{}, err
	}
	added, err := writeServer(configDir, sc, os.O_TRUNC)
	if err != nil {
		return AddedServer{}, err
	}
	added.ProjectionPath = InstallBundledProjection(configDir, sc)
	return added, nil
}

func RemoveServer(configDir, name string) error {
	if err := validServerName(name); err != nil {
		return err
	}
	// The server file goes last: if cleanup fails, the server stays configured and the remove can be retried.
	if err := forgetStateStoredByName(configDir, name); err != nil {
		return err
	}
	if err := os.Remove(projectionPath(configDir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s projections: %w", name, err)
	}
	path := serverPath(configDir, name)
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func forgetStateStoredByName(configDir, name string) error {
	if err := os.Remove(config.ServerMetaPath(configDir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("forget %s state: %w", name, err)
	}
	if err := auth.DeleteCredentials(configDir, name); err != nil {
		return fmt.Errorf("forget %s credentials: %w", name, err)
	}
	return nil
}

func writeServer(configDir string, sc config.ServerConfig, openFlag int) (AddedServer, error) {
	written, defaultPermissions := withBundledPermissions(sc)
	path := serverPath(configDir, sc.Name)
	data, err := yaml.Marshal(written)
	if err != nil {
		return AddedServer{}, err
	}
	if err := config.ValidateServerFile(path, data); err != nil {
		return AddedServer{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return AddedServer{}, fmt.Errorf("create servers dir: %w", err)
	}
	if err := writeNewOrTruncate(path, data, openFlag); err != nil {
		return AddedServer{}, fmt.Errorf("write %s: %w", path, err)
	}
	return AddedServer{Config: written, Path: path, DefaultPermissions: defaultPermissions}, nil
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

func serverPath(configDir, name string) string {
	return filepath.Join(configDir, "servers", name+".yaml")
}

func projectionPath(configDir, name string) string {
	return filepath.Join(configDir, "servers", name+".proj.yaml")
}

func validServerName(name string) error {
	if !config.ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", name)
	}
	return nil
}
