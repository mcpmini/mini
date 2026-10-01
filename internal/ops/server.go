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

// AddServer never replaces a server file: a configured name returns ErrAlreadyConfigured, unless
// sc itself is invalid, which is reported first.
func AddServer(configDir string, sc config.ServerConfig) (AddedServer, error) {
	if err := validServerName(sc.Name); err != nil {
		return AddedServer{}, err
	}
	added, err := writeServer(configDir, sc)
	if errors.Is(err, fs.ErrExist) {
		err = fmt.Errorf("%s is %w", sc.Name, ErrAlreadyConfigured)
	}
	if err != nil {
		return AddedServer{}, err
	}
	if err := forgetStateStoredByName(configDir, sc.Name); err != nil {
		return AddedServer{}, errors.Join(err, os.Remove(added.Path))
	}
	added.ProjectionPath, err = InstallBundledProjection(configDir, sc)
	if err != nil {
		return AddedServer{}, errors.Join(err, os.Remove(added.Path))
	}
	return added, nil
}

func RemoveServer(configDir, name string) error {
	if err := validServerName(name); err != nil {
		return err
	}
	path := config.ServerPath(configDir, name)
	if !config.ServerFileExists(configDir, name) {
		return fmt.Errorf("remove %s: %w", path, fs.ErrNotExist)
	}
	// The server file goes last: if cleanup fails, the server stays configured and the remove can be retried.
	if err := forgetStateStoredByName(configDir, name); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func forgetStateStoredByName(configDir, name string) error {
	if err := os.Remove(config.ServerMetaPath(configDir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("forget %s state: %w", name, err)
	}
	if err := os.Remove(config.ProjectionPath(configDir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("forget %s projections: %w", name, err)
	}
	if err := auth.DeleteCredentials(configDir, name); err != nil {
		return fmt.Errorf("forget %s credentials: %w", name, err)
	}
	return nil
}

func writeServer(configDir string, sc config.ServerConfig) (AddedServer, error) {
	written, defaultPermissions := withBundledPermissions(sc)
	path := config.ServerPath(configDir, sc.Name)
	data, err := yaml.Marshal(written)
	if err != nil {
		return AddedServer{}, err
	}
	if err := config.ValidateServerFile(path, data); err != nil {
		return AddedServer{}, err
	}
	if err := writeNewFile(path, data); err != nil {
		return AddedServer{}, fmt.Errorf("write %s: %w", path, err)
	}
	return AddedServer{Config: written, Path: path, DefaultPermissions: defaultPermissions}, nil
}

func writeNewFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	// Adds never replace a file, so a partly written one would block every later add.
	if err != nil {
		os.Remove(path) //nolint:errcheck
	}
	return err
}

func validServerName(name string) error {
	if !config.ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", name)
	}
	return nil
}
