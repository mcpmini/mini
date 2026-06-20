package projection

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mcpmini/mini/internal/config"
)

type Truncation struct {
	JQPath string `json:"path"` // e.g. .files[3].patch
	Bytes  int    `json:"bytes"`
}

type Result struct {
	Summary     any
	ElidedKeys  []string
	Passthrough map[string]any
	Truncated    []Truncation
	Hint        string
}

type projCtx struct {
	cfg   *effectiveConfig
	depth int
	path  []string
}

// Apply projects a parsed JSON value using the given config.
// Falls back to defaults when cfg is nil.
func Apply(value any, cfg *config.ProjectionConfig, defaults *Defaults) Result {
	effective := mergeWithDefaults(cfg, defaults)
	ctx := projCtx{cfg: effective, depth: 0, path: nil}
	var omitted []Truncation
	summary := project(value, ctx, &omitted)
	elided := collectElided(value, summary, "")
	passthrough := extractPassthrough(value, effective.passthrough)
	sortOmissions(omitted)
	return Result{
		Summary:     summary,
		ElidedKeys:  elided,
		Passthrough: passthrough,
		Truncated:     omitted,
		Hint:        effective.hint,
	}
}

func sortOmissions(omitted []Truncation) {
	sort.Slice(omitted, func(i, j int) bool { return omitted[i].JQPath < omitted[j].JQPath })
}

func project(value any, ctx projCtx, omitted *[]Truncation) any {
	if ctx.cfg.depthLimit > 0 && ctx.depth >= ctx.cfg.depthLimit {
		return "[depth limit reached]"
	}

	switch v := value.(type) {
	case map[string]any:
		return projectMap(v, ctx, omitted)
	case []any:
		return projectArray(v, ctx, omitted)
	case string:
		return projectString(v, ctx, "", omitted)
	default:
		return value
	}
}

func projectMap(m map[string]any, ctx projCtx, omitted *[]Truncation) map[string]any {
	out := make(map[string]any)
	for k, v := range m {
		if shouldSkipField(k, ctx.cfg, ctx.depth) {
			continue
		}
		out[k] = projectMapValue(v, ctx, k, omitted)
	}
	return out
}

func shouldSkipField(key string, cfg *effectiveConfig, depth int) bool {
	if isExcluded(key, cfg.excludeAlways) {
		return true
	}
	return depth == 0 && len(cfg.include) > 0 && !isIncluded(key, cfg.include) && !isPassthrough(key, cfg.passthrough)
}

func projectMapValue(value any, ctx projCtx, fieldName string, omitted *[]Truncation) any {
	// Cloning the path because we're recursing during field path traversal
	childPath := append(slices.Clone(ctx.path), fieldName)
	switch sv := value.(type) {
	case string:
		return projectString(sv, projCtx{cfg: ctx.cfg, depth: ctx.depth, path: childPath}, fieldName, omitted)
	case map[string]any:
		return project(sv, projCtx{cfg: ctx.cfg, depth: ctx.depth + 1, path: childPath}, omitted)
	case []any:
		return projectNamedArray(sv, projCtx{cfg: ctx.cfg, depth: ctx.depth + 1, path: childPath}, fieldName, omitted)
	default:
		return value
	}
}

func projectArray(arr []any, ctx projCtx, omitted *[]Truncation) []any {
	return projectNamedArray(arr, ctx, "", omitted)
}

func projectNamedArray(arr []any, ctx projCtx, fieldName string, omitted *[]Truncation) []any {
	arr, original := truncateArray(arr, ctx.cfg.arrayLimitFor(fieldName))
	out := make([]any, len(arr), projectedArrayCap(len(arr), original))
	for i, v := range arr {
		// Cloning the path because we're recursing during field path traversal
		itemCtx := projCtx{cfg: ctx.cfg, depth: ctx.depth, path: append(slices.Clone(ctx.path), fmt.Sprintf("[%d]", i))}
		out[i] = project(v, itemCtx, omitted)
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

func projectString(s string, ctx projCtx, fieldName string, omitted *[]Truncation) string {
	if ctx.cfg.stripContent || (ctx.cfg.autoStripThreshold > 0 && len(s) >= ctx.cfg.autoStripThreshold && ctx.cfg.contentFieldSet[fieldName]) {
		s = StripMarkup(s)
	}
	if omitLimit := ctx.cfg.omitLimitFor(fieldName); omitLimit > 0 && len(s) > omitLimit {
		return replaceWithPlaceholder(s, formatPath(ctx.path), omitted)
	}
	limit := ctx.cfg.stringLimitFor(fieldName)
	if limit > 0 && len(s) > limit {
		cut := truncateAtBoundary(s, limit)
		recordOmission(formatPath(ctx.path), len(s)-len(cut), omitted)
		return cut
	}
	return s
}

func formatPath(path []string) string {
	if len(path) == 0 {
		return ""
	}
	var b strings.Builder
	for _, seg := range path {
		if strings.HasPrefix(seg, "[") {
			b.WriteString(seg)
		} else {
			b.WriteByte('.')
			b.WriteString(seg)
		}
	}
	return b.String()
}

func replaceWithPlaceholder(s, path string, omitted *[]Truncation) string {
	recordOmission(path, len(s), omitted)
	if path == "" {
		return fmt.Sprintf("<omitted: %d chars — see raw>", len(s))
	}
	return fmt.Sprintf("<omitted: %d chars — see raw, path %s>", len(s), path)
}

func recordOmission(path string, bytes int, omitted *[]Truncation) {
	*omitted = append(*omitted, Truncation{JQPath: path, Bytes: bytes})
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
