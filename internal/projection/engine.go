package projection

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mcpmini/mini/internal/config"
)

// Omission records a field whose value was replaced with a placeholder
// because it exceeded an omit_limits threshold. Path is a dot/bracket
// pointer into the projected response (e.g. ".files[3].patch").
type Omission struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

type Result struct {
	Summary     any
	ElidedKeys  []string
	Passthrough map[string]any
	Omitted     []Omission
	Hint        string
}

// projCtx carries the merged config plus per-call traversal state. path is
// passed by value so each recursive call gets its own slice — no push/pop
// bookkeeping and no shared-backing-array aliasing bugs.
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
	var omitted []Omission
	summary := project(value, ctx, &omitted)
	elided := collectElided(value, summary, "")
	passthrough := extractPassthrough(value, effective.passthrough)
	sortOmissions(omitted)
	return Result{
		Summary:     summary,
		ElidedKeys:  elided,
		Passthrough: passthrough,
		Omitted:     omitted,
		Hint:        effective.hint,
	}
}

func sortOmissions(omitted []Omission) {
	sort.Slice(omitted, func(i, j int) bool { return omitted[i].Path < omitted[j].Path })
}

func project(value any, ctx projCtx, omitted *[]Omission) any {
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

func projectMap(m map[string]any, ctx projCtx, omitted *[]Omission) map[string]any {
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

func projectMapValue(value any, ctx projCtx, fieldName string, omitted *[]Omission) any {
	childPath := append(slices.Clone(ctx.path), fieldName)
	switch sv := value.(type) {
	case string:
		return projectString(sv, ctx, fieldName, omitted, childPath)
	case map[string]any:
		return project(sv, projCtx{cfg: ctx.cfg, depth: ctx.depth + 1, path: childPath}, omitted)
	case []any:
		return projectNamedArray(sv, projCtx{cfg: ctx.cfg, depth: ctx.depth + 1, path: childPath}, fieldName, omitted)
	default:
		return value
	}
}

func projectArray(arr []any, ctx projCtx, omitted *[]Omission) []any {
	return projectNamedArray(arr, ctx, "", omitted)
}

func projectNamedArray(arr []any, ctx projCtx, fieldName string, omitted *[]Omission) []any {
	arr, original := truncateArray(arr, ctx.cfg.arrayLimitFor(fieldName))
	out := make([]any, len(arr), projectedArrayCap(len(arr), original))
	for i, v := range arr {
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

// projectString applies markup stripping, then omit_limits (full
// replacement with a placeholder + path pointer), then string_limits
// (boundary-aware truncation). path, if provided, is the field's full
// pointer into the response; omission records use it verbatim.
func projectString(s string, ctx projCtx, fieldName string, omitted *[]Omission, path ...[]string) string {
	if ctx.cfg.stripContent || (ctx.cfg.autoStripThreshold > 0 && len(s) >= ctx.cfg.autoStripThreshold && ctx.cfg.contentFieldSet[fieldName]) {
		s = StripMarkup(s)
	}
	if omitLimit := ctx.cfg.omitLimitFor(fieldName); omitLimit > 0 && len(s) > omitLimit {
		return replaceWithPlaceholder(s, fieldPath(path), omitted)
	}
	limit := ctx.cfg.stringLimitFor(fieldName)
	if limit > 0 && len(s) > limit {
		cut := truncateAtBoundary(s, limit)
		recordOmission(fieldPath(path), len(s)-len(cut), omitted)
		return cut
	}
	return s
}

func fieldPath(path [][]string) string {
	if len(path) == 0 || len(path[0]) == 0 {
		return ""
	}
	var b strings.Builder
	for _, seg := range path[0] {
		if strings.HasPrefix(seg, "[") {
			b.WriteString(seg)
		} else {
			b.WriteByte('.')
			b.WriteString(seg)
		}
	}
	return b.String()
}

func replaceWithPlaceholder(s, path string, omitted *[]Omission) string {
	recordOmission(path, len(s), omitted)
	if path == "" {
		return fmt.Sprintf("<omitted: %d chars — see raw>", len(s))
	}
	return fmt.Sprintf("<omitted: %d chars — see raw, path %s>", len(s), path)
}

func recordOmission(path string, bytes int, omitted *[]Omission) {
	*omitted = append(*omitted, Omission{Path: path, Bytes: bytes})
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
