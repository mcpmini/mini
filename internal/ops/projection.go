package ops

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/defaults"
)

func DetectProjectionKey(sc config.ServerConfig) string {
	cmdLine := strings.ToLower(sc.Command + " " + strings.Join(sc.Args, " "))
	return defaults.DetectKey(cmdLine, sc.URL)
}

func InstallBundledProjection(configDir string, sc config.ServerConfig) string {
	key := DetectProjectionKey(sc)
	if key == "" {
		return ""
	}
	bundled := defaults.ProjectionFor(key)
	if bundled == nil {
		return ""
	}
	dest := filepath.Join(configDir, "servers", sc.Name+".proj.yaml")
	if !writeBundledProjection(dest, bundled) {
		return ""
	}
	return dest
}

func writeBundledProjection(dest string, data []byte) bool {
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return false
	}
	if projectionExists(dest) {
		return false
	}
	return os.WriteFile(dest, data, 0600) == nil
}

func projectionExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func WithBundledPermissions(sc config.ServerConfig) config.ServerConfig {
	if sc.Permissions == nil {
		sc.Permissions = loadBundledPermissions(sc)
	}
	return sc
}

func loadBundledPermissions(sc config.ServerConfig) *config.PermissionsConfig {
	key := DetectProjectionKey(sc)
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
