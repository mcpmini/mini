package ops

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/defaults"
)

func withBundledProjections(sc config.ServerConfig) (config.ServerConfig, bool, error) {
	if sc.Projections != nil {
		return sc, false, nil
	}
	key := defaults.MatchKnownServer(sc.Command, sc.Args, sc.URL)
	data := defaults.ProjectionFor(key)
	if data == nil {
		return sc, false, nil
	}
	var projections map[string]*config.ProjectionConfig
	if err := yaml.Unmarshal(data, &projections); err != nil {
		return sc, false, fmt.Errorf("bundled projections for %s: %w", key, err)
	}
	sc.Projections = projections
	return sc, true, nil
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
