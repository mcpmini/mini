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
		wk := CollapseIndex(p)
		if !seen[wk] {
			seen[wk] = true
			out = append(out, wk)
		}
	}
	return out
}

// CollapseIndex replaces numeric array indices [N] with [] in a jq path.
// Quoted keys like ["foo[0]bar"] are copied verbatim — [N] inside a quoted
// key is part of the key name, not an array index.
func CollapseIndex(p string) string {
	if !strings.ContainsRune(p, '[') {
		return p
	}
	var b strings.Builder
	i := 0
	for i < len(p) {
		if p[i] == '[' && i+1 < len(p) && p[i+1] == '"' {
			b.WriteString(`["`)
			i += 2
			for i < len(p) {
				if p[i] == '\\' && i+1 < len(p) { // skip \" so it isn't mistaken for the closing "
					b.WriteByte(p[i])
					b.WriteByte(p[i+1])
					i += 2
					continue
				}
				if p[i] == '"' && i+1 < len(p) && p[i+1] == ']' {
					b.WriteString(`"]`)
					i += 2
					break
				}
				b.WriteByte(p[i])
				i++
			}
			continue
		}
		if p[i] == '[' {
			j := i + 1
			for j < len(p) && p[j] >= '0' && p[j] <= '9' {
				j++
			}
			if j > i+1 && j < len(p) && p[j] == ']' {
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
