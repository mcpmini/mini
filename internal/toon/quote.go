package toon

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// numericLikeRE mirrors spec §7.2's /^[+-]?\d+(?:\.\d+)?(?:e[+-]?\d+)?$/i.
// See https://github.com/toon-format/spec/blob/main/SPEC.md#72-quoting-rules-for-string-values
var numericLikeRE = regexp.MustCompile(`(?i)^[+-]?\d+(\.\d+)?(e[+-]?\d+)?$`)

// unquotedKeyRE mirrors spec §7.3's ^[A-Za-z_][A-Za-z0-9_.]*$.
// See https://github.com/toon-format/spec/blob/main/SPEC.md#73-key-encoding
var unquotedKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

// structuralChars is spec §7.2's always-quote set (colon, quote, backslash,
// brackets/braces) plus the document delimiter, hardcoded to comma per 1a's
// locked options (no delimiter option plumbing yet).
// See https://github.com/toon-format/spec/blob/main/SPEC.md#72-quoting-rules-for-string-values
const structuralChars = ":\"\\[]{},"

func encodeString(s string) string {
	if needsQuoting(s) {
		return quoteString(s)
	}
	return s
}

func validateUTF8(s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("toon: string contains invalid UTF-8")
	}
	return nil
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
	if hasLeadingOrTrailingASCIISpace(s) {
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
	if strings.HasPrefix(s, "#") {
		return true
	}
	return strings.HasPrefix(s, "-")
}

// Spec §12 trims exactly U+0020; the broader ASCII check is harmless since
// control chars (tab, newline, etc.) are already caught by containsControlChar.
func hasLeadingOrTrailingASCIISpace(s string) bool {
	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)
	return isASCIISpace(first) || isASCIISpace(last)
}

func isASCIISpace(r rune) bool {
	return r < 0x80 && (r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v')
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
