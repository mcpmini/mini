package projection

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/jq"
)

type Truncation struct {
	JQPath string `json:"path"` // e.g. .files[3].patch
	Chars  int    `json:"chars,omitempty"`
	Items  int    `json:"items,omitempty"`
}

type Result struct {
	Summary      any
	ExcludedKeys []string
	Passthrough  map[string]any
	Truncated    []Truncation
}

type projCtx struct {
	cfg       *effectiveConfig
	depth     int
	path      []string
	excluded  *[]string
	truncated *[]Truncation
}

// Apply projects a parsed JSON value using the given config.
// Falls back to defaults when cfg is nil.
func Apply(value any, cfg *config.ProjectionConfig, defaults *Defaults) Result {
	effective := mergeWithDefaults(cfg, defaults)
	var excluded []string
	var truncated []Truncation
	ctx := projCtx{cfg: effective, excluded: &excluded, truncated: &truncated}
	summary := project(value, ctx)
	passthrough := extractPassthrough(value, effective.passthrough)
	slices.Sort(excluded)
	sortTruncated(truncated)
	return Result{
		Summary:      summary,
		ExcludedKeys: collapseExcluded(excluded),
		Passthrough:  passthrough,
		Truncated:    truncated,
	}
}

func sortTruncated(truncated []Truncation) {
	slices.SortFunc(truncated, func(a, b Truncation) int { return strings.Compare(a.JQPath, b.JQPath) })
}

func project(value any, ctx projCtx) any {
	if ctx.cfg.depthLimit > 0 && ctx.depth >= ctx.cfg.depthLimit {
		return "[depth limit reached]"
	}

	switch v := value.(type) {
	case map[string]any:
		return projectMap(v, ctx)
	case []any:
		return projectArray(v, ctx)
	case string:
		return projectString(v, ctx, "")
	default:
		return value
	}
}

func projectMap(m map[string]any, ctx projCtx) map[string]any {
	out := make(map[string]any)
	for k, v := range m {
		if shouldSkipField(k, ctx.cfg, ctx.depth) {
			*ctx.excluded = append(*ctx.excluded, jq.FormatPath(append(slices.Clone(ctx.path), k)))
			continue
		}
		out[k] = projectMapValue(v, ctx, k)
	}
	return out
}

func shouldSkipField(key string, cfg *effectiveConfig, depth int) bool {
	if isExcluded(key, cfg.exclude) {
		return true
	}
	return depth == 0 && len(cfg.includeOnly) > 0 && !isIncluded(key, cfg.includeOnly) && !isPassthrough(key, cfg.passthrough)
}

func projectMapValue(value any, ctx projCtx, fieldName string) any {
	childPath := append(slices.Clone(ctx.path), fieldName)
	switch sv := value.(type) {
	case string:
		return projectString(sv, projCtx{cfg: ctx.cfg, depth: ctx.depth, path: childPath, excluded: ctx.excluded, truncated: ctx.truncated}, fieldName)
	case map[string]any:
		return project(sv, projCtx{cfg: ctx.cfg, depth: ctx.depth + 1, path: childPath, excluded: ctx.excluded, truncated: ctx.truncated})
	case []any:
		return projectNamedArray(sv, projCtx{cfg: ctx.cfg, depth: ctx.depth + 1, path: childPath, excluded: ctx.excluded, truncated: ctx.truncated}, fieldName)
	default:
		return value
	}
}

func projectArray(arr []any, ctx projCtx) []any {
	return projectNamedArray(arr, ctx, "")
}

func projectNamedArray(arr []any, ctx projCtx, fieldName string) []any {
	arr, original := truncateArray(arr, ctx.cfg.arrayLimitFor(fieldName))
	out := make([]any, len(arr))
	for i, v := range arr {
		itemCtx := projCtx{cfg: ctx.cfg, depth: ctx.depth, path: append(slices.Clone(ctx.path), fmt.Sprintf("[%d]", i)), excluded: ctx.excluded, truncated: ctx.truncated}
		out[i] = project(v, itemCtx)
	}
	if len(arr) < original {
		*ctx.truncated = append(*ctx.truncated, Truncation{JQPath: jq.FormatPath(ctx.path), Items: original - len(arr)})
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

func projectString(s string, ctx projCtx, fieldName string) string {
	if ctx.cfg.stripContent || (ctx.cfg.autoStripThreshold > 0 && len(s) >= ctx.cfg.autoStripThreshold && ctx.cfg.contentFieldSet[fieldName]) {
		s = StripMarkup(s)
	}
	limit := ctx.cfg.stringLimitFor(fieldName)
	if limit > 0 {
		runeCount := utf8.RuneCountInString(s)
		if runeCount > limit {
			cut := truncateAtBoundary(s, limit)
			*ctx.truncated = append(*ctx.truncated, Truncation{JQPath: jq.FormatPath(ctx.path), Chars: runeCount - utf8.RuneCountInString(cut)})
			return cut
		}
	}
	return s
}

func isIncluded(key string, includeOnly []string) bool { return slices.Contains(includeOnly, key) }
func isPassthrough(key string, pt []string) bool       { return slices.Contains(pt, key) }

func isExcluded(key string, exclude []string) bool {
	for _, k := range exclude {
		top, _, _ := strings.Cut(k, ".")
		top = strings.TrimSuffix(top, "[]")
		if top == key {
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

// truncateAtBoundary cuts at a word/sentence boundary near runeLimit.
// Boundary searches use ASCII bytes ('.', '\n', ' ') which never appear as
// UTF-8 continuation bytes, so cuts always land on rune boundaries.
func truncateAtBoundary(s string, runeLimit int) string {
	byteLimit := runeOffset(s, runeLimit)
	if byteLimit >= len(s) {
		return s
	}
	if sentenceCut := scanBackward(s, byteLimit, 100, func(b byte) bool { return b == '.' || b == '\n' }); sentenceCut >= 0 {
		return s[:sentenceCut+1]
	}
	if wordCut := scanBackward(s, byteLimit, 50, func(b byte) bool { return b == ' ' }); wordCut >= 0 {
		return s[:wordCut]
	}
	return s[:byteLimit]
}

func runeOffset(s string, n int) int {
	i := 0
	for range n {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if i >= len(s) {
			return len(s)
		}
	}
	return i
}

func scanBackward(s string, limit, window int, match func(byte) bool) int {
	for i := limit; i > limit-window && i > 0; i-- {
		if match(s[i]) {
			return i
		}
	}
	return -1
}
