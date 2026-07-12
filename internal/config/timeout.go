package config

import (
	"fmt"
	"time"
)

// ParseTimeoutSpec parses the "empty means def, \"0\" disables, otherwise a
// positive duration" convention shared by mini's *_timeout config fields
// (tool_timeout, http_client_timeout, connect_timeout). enabled is false
// when the timeout is disabled; d is only meaningful when enabled is true.
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
