package config

import (
	"fmt"
	"regexp"
)

var excludePathPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+(\.[a-zA-Z0-9_-]+)*$`)

// ValidExcludePath accepts dotted object-field paths relative to the projected root, without array traversal.
func ValidExcludePath(path string) bool {
	return excludePathPattern.MatchString(path)
}

// ValidateProjection checks projection rules before they are persisted or published.
func ValidateProjection(p *ProjectionConfig) error {
	if p == nil {
		return nil
	}
	if err := ValidResponseFormat(p.Format); err != nil {
		return fmt.Errorf("format: %w", err)
	}
	for _, path := range p.Exclude {
		if !ValidExcludePath(path) {
			return fmt.Errorf(
				"invalid exclude path %q: use dot-separated object fields containing only ASCII letters, digits, underscores or hyphens, without a leading dot; array traversal is unsupported",
				path,
			)
		}
	}
	return nil
}
