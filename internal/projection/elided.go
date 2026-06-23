package projection

import "strings"

// collapseElided deduplicates elided jq paths by converting numeric array
// indices to [] wildcards, so .items[0].secret and .items[1].secret both
// become .items[].secret and are reported once.
func collapseElided(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		wk := indexedToWildcard(p)
		if !seen[wk] {
			seen[wk] = true
			out = append(out, wk)
		}
	}
	return out
}

func indexedToWildcard(p string) string {
	if !strings.ContainsRune(p, '[') {
		return p
	}
	var b strings.Builder
	i := 0
	for i < len(p) {
		if p[i] == '[' {
			j := i + 1
			for j < len(p) && p[j] >= '0' && p[j] <= '9' {
				j++
			}
			if j < len(p) && p[j] == ']' && j > i+1 {
				b.WriteString("[]")
				i = j + 1
				continue
			}
		}
		b.WriteByte(p[i])
		i++
	}
	return b.String()
}
