package toon

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// numericLikeRE mirrors spec §7.2's /^-?\d+(?:\.\d+)?(?:e[+-]?\d+)?$/i.
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#72-quoting-rules-for-string-values
var numericLikeRE = regexp.MustCompile(`(?i)^-?\d+(\.\d+)?(e[+-]?\d+)?$`)

// unquotedKeyRE mirrors spec §7.3's ^[A-Za-z_][A-Za-z0-9_.]*$.
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#73-key-encoding
var unquotedKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// structuralChars is spec §7.2's always-quote set (colon, quote, backslash,
// brackets/braces) plus the document delimiter, hardcoded to comma per 1a's
// locked options (no delimiter option plumbing yet).
// See https://github.com/toon-format/spec/blob/f55b93ac489f297ff597d95e4c19ae84675eaeb7/SPEC.md#72-quoting-rules-for-string-values
const structuralChars = ":\"\\[]{},"

func encodeString(s string) string {
	if needsQuoting(s) {
		return quoteString(s)
	}
	return s
}

func encodeKey(key string) string {
	if unquotedKeyRE.MatchString(key) {
		return key
	}
	return quoteString(key)
}

func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	if hasLeadingOrTrailingWhitespace(s) {
		return true
	}
	if s == "true" || s == "false" || s == "null" {
		return true
	}
	if numericLikeRE.MatchString(s) {
		return true
	}
	if strings.ContainsAny(s, structuralChars) {
		return true
	}
	if containsControlChar(s) {
		return true
	}
	return strings.HasPrefix(s, "-")
}

func hasLeadingOrTrailingWhitespace(s string) bool {
	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(first) || unicode.IsSpace(last)
}

func containsControlChar(s string) bool {
	for _, r := range s {
		if r <= 0x1F {
			return true
		}
	}
	return false
}

func quoteString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		writeEscaped(&sb, r)
	}
	sb.WriteByte('"')
	return sb.String()
}

func writeEscaped(sb *strings.Builder, r rune) {
	switch r {
	case '\\':
		sb.WriteString(`\\`)
	case '"':
		sb.WriteString(`\"`)
	case '\n':
		sb.WriteString(`\n`)
	case '\r':
		sb.WriteString(`\r`)
	case '\t':
		sb.WriteString(`\t`)
	default:
		if r <= 0x1F {
			fmt.Fprintf(sb, `\u%04x`, r)
			return
		}
		sb.WriteRune(r)
	}
}
