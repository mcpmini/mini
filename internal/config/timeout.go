package config

import (
	"fmt"
	"time"
)

// ParseTimeoutSpec decodes the three-way timeout convention: "" returns def, "0" disables
// (enabled=false), any other value is parsed as a positive duration. d is only meaningful
// when enabled is true.
func ParseTimeoutSpec(spec string, def time.Duration) (d time.Duration, enabled bool, err error) {
	switch spec {
	case "":
		return def, true, nil
	case "0":
		return 0, false, nil
	}
	d, err = time.ParseDuration(spec)
	if err != nil {
		return 0, false, fmt.Errorf("invalid duration %q: %w", spec, err)
	}
	if d <= 0 {
		return 0, false, fmt.Errorf("invalid duration %q: must be positive", spec)
	}
	return d, true, nil
}
