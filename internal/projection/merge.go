package projection

import "github.com/mcpmini/mini/internal/config"

// Defaults are the global fallback limits when no projection config exists.
type Defaults struct {
	StringLimit        int
	DepthLimit         int
	ContentFields      []string // field names auto-stripped when >= AutoStripThreshold chars
	AutoStripThreshold int      // 0 = disabled
}

func DefaultsFrom(cfg *config.Config) *Defaults {
	return &Defaults{
		StringLimit:        cfg.DefaultStringLimit,
		DepthLimit:         cfg.DefaultDepthLimit,
		ContentFields:      cfg.ContentFields,
		AutoStripThreshold: cfg.AutoStripThreshold,
	}
}

// effectiveConfig is the merged, runtime-ready config derived from ProjectionConfig + Defaults.
type effectiveConfig struct {
	includeOnly        []string
	excludeAlways      []string
	passthrough        []string
	arrayLimits        map[string]int
	stringLimits       map[string]int
	omitLimits         map[string]int
	defaultArrayLimit  int
	defaultStringLimit int
	depthLimit         int
	stripContent       bool
	contentFieldSet    map[string]bool // precomputed set for O(1) lookup
	autoStripThreshold int
}

const (
	slimStringLimit = 200
	slimArrayLimit  = 3
	slimDepthLimit  = 2
)

func (c *effectiveConfig) stringLimitFor(field string) int {
	if v, ok := c.stringLimits[field]; ok {
		return v
	}
	return c.defaultStringLimit
}

func (c *effectiveConfig) arrayLimitFor(field string) int {
	if field != "" {
		if v, ok := c.arrayLimits[field]; ok {
			return v
		}
	}
	return c.defaultArrayLimit
}

func mergeWithDefaults(cfg *config.ProjectionConfig, d *Defaults) *effectiveConfig {
	e := effectiveFromDefaults(d)
	if cfg == nil {
		return e
	}
	applyProjectionConfig(e, cfg)
	return e
}

func effectiveFromDefaults(d *Defaults) *effectiveConfig {
	e := &effectiveConfig{
		defaultStringLimit: d.StringLimit,
		depthLimit:         d.DepthLimit,
		autoStripThreshold: d.AutoStripThreshold,
	}
	if len(d.ContentFields) > 0 {
		e.contentFieldSet = make(map[string]bool, len(d.ContentFields))
		for _, f := range d.ContentFields {
			e.contentFieldSet[f] = true
		}
	}
	return e
}

func (c *effectiveConfig) omitLimitFor(field string) int {
	return c.omitLimits[field]
}

func applyProjectionConfig(e *effectiveConfig, cfg *config.ProjectionConfig) {
	if cfg.Mode == "slim" {
		applySlimMode(e)
	}
	e.includeOnly = cfg.IncludeOnly
	e.excludeAlways = cfg.ExcludeAlways
	e.passthrough = cfg.Passthrough
	e.arrayLimits = cfg.ArrayLimits
	e.stringLimits = cfg.StringLimits
	e.omitLimits = cfg.OmitLimits
	if cfg.DepthLimit > 0 {
		e.depthLimit = cfg.DepthLimit
	}
	if cfg.StripMarkup {
		e.stripContent = true
	}
}

func applySlimMode(e *effectiveConfig) {
	e.defaultStringLimit = slimStringLimit
	e.defaultArrayLimit = slimArrayLimit
	e.depthLimit = slimDepthLimit
	e.stripContent = true
}
