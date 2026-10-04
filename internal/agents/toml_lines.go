package agents

import (
	"strings"

	"github.com/BurntSushi/toml"
)

type tomlLine struct {
	text       string
	header     []string
	arrayTable bool
	key        []string
	value      string
	table      []string
}

func (l tomlLine) isHeader() bool { return l.header != nil }

type tomlScanner struct {
	openString string
	depth      int
	table      []string
}

func scanTOMLLines(lines []string) []tomlLine {
	var s tomlScanner
	scanned := make([]tomlLine, len(lines))
	for i, text := range lines {
		scanned[i] = s.scan(text)
	}
	return scanned
}

func (s *tomlScanner) scan(text string) tomlLine {
	line := tomlLine{text: text, table: s.table}
	atTopLevel := s.openString == "" && s.depth == 0
	rest := s.skipOpenString(text)
	if !atTopLevel {
		s.track(rest)
		return line
	}
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "[") {
		line.header, line.arrayTable = parseTOMLHeader(trimmed)
		if line.header != nil {
			s.table = line.header
			line.table = line.header
		}
		return line
	}
	if key, value, ok := parseTOMLKey(trimmed); ok {
		line.key, line.value = key, strings.TrimSpace(value)
		s.track(value)
	}
	return line
}

func (s *tomlScanner) skipOpenString(text string) string {
	if s.openString == "" {
		return text
	}
	end := closingQuoteEndIndex(text, s.openString)
	if end < 0 {
		return ""
	}
	s.openString = ""
	return text[end:]
}

func (s *tomlScanner) track(text string) {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '#':
			return
		case '"', '\'':
			i = s.skipString(text, i)
		case '[', '{':
			s.depth++
		case ']', '}':
			s.depth--
		}
	}
}

func (s *tomlScanner) skipString(text string, start int) int {
	quote := text[start : start+1]
	if strings.HasPrefix(text[start:], strings.Repeat(quote, 3)) {
		quote = strings.Repeat(quote, 3)
	}
	end := closingQuoteEndIndex(text[start+len(quote):], quote)
	if end < 0 {
		if len(quote) == 3 {
			s.openString = quote
		}
		return len(text)
	}
	return start + len(quote) + end - 1
}

func closingQuoteEndIndex(text, quote string) int {
	for i := 0; i < len(text); i++ {
		if quote[0] == '"' && text[i] == '\\' {
			i++
			continue
		}
		if strings.HasPrefix(text[i:], quote) {
			return i + len(quote)
		}
	}
	return -1
}

func parseTOMLHeader(trimmed string) ([]string, bool) {
	arrayTable := strings.HasPrefix(trimmed, "[[")
	open, closing := "[", "]"
	if arrayTable {
		open, closing = "[[", "]]"
	}
	key, rest, ok := parseTOMLDottedKey(trimmed[len(open):])
	rest = strings.TrimSpace(rest)
	if !ok || !strings.HasPrefix(rest, closing) {
		return nil, false
	}
	after := strings.TrimSpace(rest[len(closing):])
	if after != "" && !strings.HasPrefix(after, "#") {
		return nil, false
	}
	return key, arrayTable
}

func parseTOMLKey(trimmed string) ([]string, string, bool) {
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return nil, "", false
	}
	key, rest, ok := parseTOMLDottedKey(trimmed)
	rest = strings.TrimSpace(rest)
	if !ok || !strings.HasPrefix(rest, "=") {
		return nil, "", false
	}
	return key, rest[1:], true
}

func parseTOMLDottedKey(text string) ([]string, string, bool) {
	var parts []string
	for {
		part, rest, ok := parseTOMLKeyPart(strings.TrimLeft(text, " \t"))
		if !ok {
			return nil, "", false
		}
		parts = append(parts, part)
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, ".") {
			return parts, rest, true
		}
		text = rest[1:]
	}
}

func parseTOMLKeyPart(text string) (string, string, bool) {
	if text == "" {
		return "", "", false
	}
	if text[0] == '"' || text[0] == '\'' {
		end := closingQuoteEndIndex(text[1:], text[:1])
		if end < 0 {
			return "", "", false
		}
		value, ok := decodeTOMLString(text[:end+1])
		return value, text[end+1:], ok
	}
	end := strings.IndexFunc(text, func(r rune) bool { return !isBareKeyRune(r) })
	if end == 0 {
		return "", "", false
	}
	if end < 0 {
		end = len(text)
	}
	return text[:end], text[end:], true
}

func isBareKeyRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-'
}

func decodeTOMLString(quoted string) (string, bool) {
	var holder struct{ V string }
	if _, err := toml.Decode("V = "+quoted, &holder); err != nil {
		return "", false
	}
	return holder.V, true
}
