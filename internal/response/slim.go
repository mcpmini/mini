package response

import (
	"encoding/json"
	"html"
	"sort"
	"strings"
)

// _meta.raw is injected by WritePair after filenames are known; Slimify does not set it.
func Slimify(data any) map[string]any {
	switch v := data.(type) {
	case []any:
		return slimArray(v)
	case map[string]any:
		if arr, key := mainArray(v); len(arr) > 0 && hasPagination(v) {
			return slimWrapped(v, arr, key)
		}
		return slimObject(v)
	default:
		return slimDocument(data)
	}
}

func slimArray(arr []any) map[string]any {
	items, fields, idx := indexedItems(arr)
	meta := map[string]any{"shape": "array", "count": len(arr), "fields": fields}
	if idx != nil {
		meta["index"] = idx
	}
	return map[string]any{"_meta": meta, "items": items}
}

func slimWrapped(v map[string]any, arr []any, _ string) map[string]any {
	result := slimArray(arr)
	meta, _ := result["_meta"].(map[string]any)
	applyPaginationMeta(meta, v)
	return result
}

func applyPaginationMeta(meta, v map[string]any) {
	if tc := coalesce(v["totalCount"], v["total_count"]); tc != nil {
		meta["total"] = tc
	}
	if pi, ok := v["pageInfo"].(map[string]any); ok {
		if cursor, _ := pi["endCursor"].(string); cursor != "" {
			meta["next_cursor"] = cursor
		}
		if hasMore, _ := pi["hasNextPage"].(bool); hasMore {
			meta["has_more"] = true
		}
	}
	if inc, _ := v["incomplete_results"].(bool); inc {
		meta["incomplete"] = true
	}
}

func slimObject(v map[string]any) map[string]any {
	flat := flatObject(v)
	result := map[string]any{"_meta": map[string]any{"shape": "object", "fields": sortedKeys(flat)}}
	for k, val := range flat {
		result[k] = val
	}
	return result
}

func slimDocument(v any) map[string]any {
	b, _ := json.Marshal(v)
	preview := string(b)
	if len(preview) > 500 {
		preview = preview[:500]
	}
	return map[string]any{
		"_meta":   map[string]any{"shape": "document", "chars": len(b)},
		"preview": preview,
	}
}

func indexedItems(arr []any) (items []any, fields []string, index map[string]any) {
	flat := flattenAll(arr)
	return toAnySlice(flat), extractFields(flat), buildIndex(flat)
}

func flattenAll(arr []any) []map[string]any {
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, flatObject(m))
		}
	}
	return out
}

func extractFields(items []map[string]any) []string {
	set := map[string]struct{}{}
	for _, m := range items {
		for k := range m {
			set[k] = struct{}{}
		}
	}
	return sortedKeySet(set)
}

func buildIndex(items []map[string]any) map[string]any {
	cats := map[string]map[string]int{}
	for _, m := range items {
		for k, v := range m {
			indexValue(cats, k, v)
		}
	}
	idx := compactIndex(cats)
	if len(idx) == 0 {
		return nil
	}
	return idx
}

func indexValue(cats map[string]map[string]int, key string, value any) {
	s, ok := value.(string)
	if !ok || len(s) >= 40 {
		return
	}
	if cats[key] == nil {
		cats[key] = map[string]int{}
	}
	cats[key][s]++
}

func compactIndex(cats map[string]map[string]int) map[string]any {
	idx := map[string]any{}
	for k, counts := range cats {
		if len(counts) > 20 {
			continue
		}
		// Skip fields where every value is unique — no pattern to surface.
		if maxCount(counts) < 2 {
			continue
		}
		idx[k] = counts
	}
	return idx
}

func maxCount(counts map[string]int) int {
	m := 0
	for _, c := range counts {
		if c > m {
			m = c
		}
	}
	return m
}

func toAnySlice(items []map[string]any) []any {
	out := make([]any, len(items))
	for i, m := range items {
		out[i] = m
	}
	return out
}

func flatObject(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		flatField(out, k, v)
	}
	return out
}

func flatField(out map[string]any, k string, v any) {
	switch child := v.(type) {
	case map[string]any:
		flatNested(out, k, child)
	case []any:
		if stringSlice(child) {
			out[k] = child
		}
	default:
		if !noiseField(k, v) && !noiseValue(v) {
			if s, ok := v.(string); ok {
				v = html.UnescapeString(s)
			}
			out[k] = v
		}
	}
}

func flatNested(out map[string]any, prefix string, child map[string]any) {
	for ck, cv := range child {
		if _, nested := cv.(map[string]any); nested {
			continue
		}
		if fk := prefix + "_" + ck; !noiseField(fk, cv) && !noiseValue(cv) {
			if s, ok := cv.(string); ok {
				cv = html.UnescapeString(s)
			}
			out[fk] = cv
		}
	}
}

func noiseField(key string, val any) bool {
	l := strings.ToLower(key)
	if isNoisyURL(l) || isTemplateURL(val) {
		return true
	}
	switch l {
	case "node_id", "gravatar_id", "sha", "git_url", "download_url",
		"upload_url", "tarball_url", "zipball_url", "assets_url":
		return true
	}
	s, ok := val.(string)
	return ok && len(s) == 40 && isHex(s)
}

func isNoisyURL(l string) bool {
	switch l {
	case "html_url", "browser_download_url", "profile_url":
		return false
	}
	return strings.HasSuffix(l, "_url")
}

func isTemplateURL(val any) bool {
	s, ok := val.(string)
	return ok && strings.Contains(s, "{/")
}

func noiseValue(v any) bool {
	switch sv := v.(type) {
	case string:
		return sv == ""
	case nil:
		return true
	}
	return false
}

func hasPagination(m map[string]any) bool {
	_, hasPageInfo := m["pageInfo"]
	_, hasTotalCount := m["total_count"]
	_, hasTotalCountAlt := m["totalCount"]
	_, hasIncomplete := m["incomplete_results"]
	return hasPageInfo || hasTotalCount || hasTotalCountAlt || hasIncomplete
}

func mainArray(m map[string]any) ([]any, string) {
	var best []any
	var key string
	for k, v := range m {
		if arr, ok := v.([]any); ok && len(arr) > len(best) {
			best, key = arr, k
		}
	}
	return best, key
}

func coalesce(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

func stringSlice(arr []any) bool {
	for _, v := range arr {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeySet(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
