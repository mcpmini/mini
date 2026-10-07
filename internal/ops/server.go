package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/mcpmini/mini/internal/auth"
	"github.com/mcpmini/mini/internal/config"
)

var ErrAlreadyConfigured = errors.New("already configured")

type AddedServer struct {
	Config             config.ServerConfig
	Path               string
	DefaultProjections bool
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
	if err := auth.DeleteCredentials(configDir, name); err != nil {
		return fmt.Errorf("forget %s credentials: %w", name, err)
	}
	return nil
}

// ValidateServer reports whether AddServer would accept sc, apart from its name being taken.
func ValidateServer(configDir string, sc config.ServerConfig) error {
	if err := validServerName(sc.Name); err != nil {
		return err
	}
	added, err := withBundledDefaults(sc)
	if err != nil {
		return err
	}
	data, err := config.EncodeServerFile(added.Config)
	if err != nil {
		return err
	}
	return config.ValidateServerFile(config.ServerPath(configDir, sc.Name), data)
}

func writeServer(configDir string, sc config.ServerConfig) (AddedServer, error) {
	added, err := withBundledDefaults(sc)
	if err != nil {
		return AddedServer{}, err
	}
	added.Path, err = config.CreateServerFile(configDir, added.Config)
	if err != nil {
		return AddedServer{}, err
	}
	return added, nil
}

func withBundledDefaults(sc config.ServerConfig) (AddedServer, error) {
	written, defaultPermissions := withBundledPermissions(sc)
	written, defaultProjections, err := withBundledProjections(written)
	return AddedServer{
		Config:             written,
		DefaultPermissions: defaultPermissions,
		DefaultProjections: defaultProjections,
	}, err
}

func validServerName(name string) error {
	if !config.ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name %q: must match ^[a-zA-Z0-9_-]+$", name)
	}
	return nil
}
