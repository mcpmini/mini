package projection

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mcpmini/mini/internal/config"
)

type Result struct {
	Summary     any
	ElidedKeys  []string
	Passthrough map[string]any
	Truncated   map[string]int // field path → bytes removed
}

// Apply projects a parsed JSON value using the given config.
// Falls back to defaults when cfg is nil.
func Apply(value any, cfg *config.ProjectionConfig, defaults *Defaults) Result {
	effective := mergeWithDefaults(cfg, defaults)
	effective.truncated = make(map[string]int)
	summary := project(value, effective, 0)
	elided := collectElided(value, summary, "")
	passthrough := extractPassthrough(value, effective.passthrough)
	return Result{
		Summary:     summary,
		ElidedKeys:  elided,
		Passthrough: passthrough,
		Truncated:   nilIfEmptyIntMap(effective.truncated),
	}
}

func nilIfEmptyIntMap(m map[string]int) map[string]int {
	if len(m) == 0 {
		return nil
	}
	return m
}

func project(value any, cfg *effectiveConfig, depth int) any {
	if cfg.depthLimit > 0 && depth >= cfg.depthLimit {
		return "[depth limit reached]"
	}

	switch v := value.(type) {
	case map[string]any:
		return projectMap(v, cfg, depth)
	case []any:
		return projectArray(v, cfg, depth)
	case string:
		return projectString(v, cfg, "")
	default:
		return value
	}
}

func projectMap(m map[string]any, cfg *effectiveConfig, depth int) map[string]any {
	out := make(map[string]any)
	for k, v := range m {
		if isExcluded(k, cfg.excludeAlways) {
			continue
		}
		// include filter only applies at the top level — nested objects pass through freely
		if depth == 0 && len(cfg.include) > 0 && !isIncluded(k, cfg.include) && !isPassthrough(k, cfg.passthrough) {
			continue
		}
		switch sv := v.(type) {
		case string:
			out[k] = projectString(sv, cfg, k)
		case map[string]any:
			out[k] = project(sv, cfg, depth+1)
		case []any:
			out[k] = projectNamedArray(sv, cfg, k, depth+1)
		default:
			out[k] = v
		}
	}
	return out
}

func projectArray(arr []any, cfg *effectiveConfig, depth int) []any {
	return projectNamedArray(arr, cfg, "", depth)
}

func projectNamedArray(arr []any, cfg *effectiveConfig, fieldName string, depth int) []any {
	original := len(arr)
	limit := cfg.arrayLimitFor(fieldName)
	if limit > 0 && len(arr) > limit {
		arr = arr[:limit]
	}
	cap := len(arr)
	if len(arr) < original {
		cap++
	}
	out := make([]any, len(arr), cap)
	for i, v := range arr {
		out[i] = project(v, cfg, depth)
	}
	if len(arr) < original {
		out = append(out, fmt.Sprintf("...+%d more", original-len(arr)))
	}
	return out
}

func projectString(s string, cfg *effectiveConfig, fieldName string) string {
	if cfg.stripContent || (cfg.autoStripThreshold > 0 && len(s) >= cfg.autoStripThreshold && cfg.contentFieldSet[fieldName]) {
		s = StripMarkup(s)
	}
	limit := cfg.stringLimitFor(fieldName)
	if limit > 0 && len(s) > limit {
		cut := truncateAtBoundary(s, limit)
		cfg.truncated[fieldName] = len(s) - len(cut)
		return cut
	}
	return s
}

func isIncluded(key string, include []string) bool  { return slices.Contains(include, key) }
func isPassthrough(key string, pt []string) bool     { return slices.Contains(pt, key) }

func isExcluded(key string, exclude []string) bool {
	for _, k := range exclude {
		// Support dot-notation "steps[].agent" — check top-level key only.
		top, _, hasDot := strings.Cut(k, ".")
		top = strings.TrimSuffix(top, "[]")
		if top == key || (!hasDot && k == key) {
			return true
		}
	}
	return false
}

func extractPassthrough(value any, keys []string) map[string]any {
	out := make(map[string]any)
	m, ok := value.(map[string]any)
	if !ok {
		return out
	}
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}

// truncateAtBoundary cuts at a word/sentence boundary near limit.
// The boundary searches are UTF-8 safe because '.', '\n', and ' ' are
// single-byte ASCII values that never appear as UTF-8 continuation bytes.
// The final fallback walks back to avoid splitting a multi-byte rune.
func truncateAtBoundary(s string, limit int) string {
	if limit >= len(s) {
		return s
	}
	for i := limit; i > limit-100 && i > 0; i-- {
		if s[i] == '.' || s[i] == '\n' {
			return s[:i+1]
		}
	}
	for i := limit; i > limit-50 && i > 0; i-- {
		if s[i] == ' ' {
			return s[:i]
		}
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}
