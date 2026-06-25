package jq

import (
	"encoding/json"
	"strings"
)

// FormatPath converts a projection path (slice of field names and "[N]" array
// index segments) to a jq filter string. Identifier-safe keys use dot notation
// (.foo); keys with spaces, punctuation, or leading digits use bracket string
// syntax (["body text"]); array index segments are passed through verbatim ([N]).
func FormatPath(path []string) string {
	if len(path) == 0 {
		return ""
	}
	var b strings.Builder
	for _, seg := range path {
		switch {
		case strings.HasPrefix(seg, "["):
			if b.Len() == 0 {
				b.WriteByte('.')
			}
			b.WriteString(seg)
		case isIdentSafe(seg):
			b.WriteByte('.')
			b.WriteString(seg)
		default:
			if b.Len() == 0 {
				b.WriteByte('.')
			}
			key, _ := json.Marshal(seg)
			b.WriteByte('[')
			b.Write(key)
			b.WriteByte(']')
		}
	}
	return b.String()
}

func isIdentSafe(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && r >= '0' && r <= '9' {
			return false
		}
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
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
			i = copyQuotedKey(&b, p, i)
			continue
		}
		if p[i] == '[' {
			if newI, ok := collapseNumericIndex(&b, p, i); ok {
				i = newI
				continue
			}
		}
		b.WriteByte(p[i])
		i++
	}
	return b.String()
}

func collapseNumericIndex(b *strings.Builder, p string, i int) (int, bool) {
	j := i + 1
	for j < len(p) && p[j] >= '0' && p[j] <= '9' {
		j++
	}
	if j > i+1 && j < len(p) && p[j] == ']' {
		b.WriteString("[]")
		return j + 1, true
	}
	return i, false
}

func copyQuotedKey(b *strings.Builder, p string, i int) int {
	b.WriteString(`["`)
	i += 2
	for i < len(p) {
		if p[i] == '\\' && i+1 < len(p) {
			b.WriteByte(p[i])
			b.WriteByte(p[i+1])
			i += 2
			continue
		}
		if p[i] == '"' && i+1 < len(p) && p[i+1] == ']' {
			b.WriteString(`"]`)
			return i + 2
		}
		b.WriteByte(p[i])
		i++
	}
	return i
}
