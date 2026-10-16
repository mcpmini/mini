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
