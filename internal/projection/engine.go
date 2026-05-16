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
		if shouldSkipField(k, cfg, depth) {
			continue
		}
		out[k] = projectMapValue(v, cfg, k, depth)
	}
	return out
}

func shouldSkipField(key string, cfg *effectiveConfig, depth int) bool {
	if isExcluded(key, cfg.excludeAlways) {
		return true
	}
	return depth == 0 && len(cfg.include) > 0 && !isIncluded(key, cfg.include) && !isPassthrough(key, cfg.passthrough)
}

func projectMapValue(value any, cfg *effectiveConfig, fieldName string, depth int) any {
	switch sv := value.(type) {
	case string:
		return projectString(sv, cfg, fieldName)
	case map[string]any:
		return project(sv, cfg, depth+1)
	case []any:
		return projectNamedArray(sv, cfg, fieldName, depth+1)
	default:
		return value
	}
}

func projectArray(arr []any, cfg *effectiveConfig, depth int) []any {
	return projectNamedArray(arr, cfg, "", depth)
}

func projectNamedArray(arr []any, cfg *effectiveConfig, fieldName string, depth int) []any {
	arr, original := truncateArray(arr, cfg.arrayLimitFor(fieldName))
	out := make([]any, len(arr), projectedArrayCap(len(arr), original))
	for i, v := range arr {
		out[i] = project(v, cfg, depth)
	}
	if len(arr) < original {
		out = append(out, fmt.Sprintf("...+%d more", original-len(arr)))
	}
	return out
}

func truncateArray(arr []any, limit int) ([]any, int) {
	original := len(arr)
	if limit > 0 && len(arr) > limit {
		arr = arr[:limit]
	}
	return arr, original
}

func projectedArrayCap(currentLen, originalLen int) int {
	if currentLen < originalLen {
		return currentLen + 1
	}
	return currentLen
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

func isIncluded(key string, include []string) bool { return slices.Contains(include, key) }
func isPassthrough(key string, pt []string) bool   { return slices.Contains(pt, key) }

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
	if sentenceCut := scanBackward(s, limit, 100, func(b byte) bool { return b == '.' || b == '\n' }); sentenceCut >= 0 {
		return s[:sentenceCut+1]
	}
	if wordCut := scanBackward(s, limit, 50, func(b byte) bool { return b == ' ' }); wordCut >= 0 {
		return s[:wordCut]
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

func scanBackward(s string, limit, window int, match func(byte) bool) int {
	for i := limit; i > limit-window && i > 0; i-- {
		if match(s[i]) {
			return i
		}
	}
	return -1
}
