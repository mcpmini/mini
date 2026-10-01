package ops

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/defaults"
)

// InstallBundledProjection returns the installed path, or "" when nothing was installed.
func InstallBundledProjection(configDir string, sc config.ServerConfig) string {
	key := defaults.MatchKnownServer(sc.Command, sc.Args, sc.URL)
	if key == "" {
		return ""
	}
	bundled := defaults.ProjectionFor(key)
	if bundled == nil {
		return ""
	}
	dest := projectionPath(configDir, sc.Name)
	if err := writeNewFile(dest, bundled); err != nil {
		return ""
	}
	return dest
}

func writeNewFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return writeNewOrTruncate(path, data, os.O_EXCL)
}

func withBundledPermissions(sc config.ServerConfig) (config.ServerConfig, bool) {
	if sc.Permissions != nil {
		return sc, false
	}
	perms := loadBundledPermissions(sc)
	if perms == nil {
		return sc, false
	}
	sc.Permissions = perms
	return sc, true
}

func loadBundledPermissions(sc config.ServerConfig) *config.PermissionsConfig {
	key := defaults.MatchKnownServer(sc.Command, sc.Args, sc.URL)
	if key == "" {
		return nil
	}
	raw := defaults.PermissionsFor(key)
	if raw == nil {
		return nil
	}
	return parsePermissions(raw)
}

func parsePermissions(raw []byte) *config.PermissionsConfig {
	var perms config.PermissionsConfig
	if err := yaml.Unmarshal(raw, &perms); err != nil {
		return nil
	}
	if len(perms.Hidden) == 0 && len(perms.Protected) == 0 {
		return nil
	}
	return &perms
}
