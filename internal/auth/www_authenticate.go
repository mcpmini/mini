package auth

import "strings"

func parseWWWAuthParam(header, param string) string {
	for _, value := range splitAuthParams(header) {
		key, paramValue, ok := strings.Cut(strings.TrimSpace(value), "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), param) {
			return unquoteAuthValue(strings.TrimSpace(paramValue))
		}
	}
	return ""
}

func splitAuthParams(header string) []string {
	startScheme := bearerStart(header)
	if startScheme < 0 {
		return nil
	}
	var parts []string
	paramsStart := startScheme + len("bearer")
	start, quoted, escaped := 0, false, false
	for i, r := range header[paramsStart:] {
		switch {
		case escaped:
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == ',' && !quoted:
			part := strings.TrimSpace(header[paramsStart+start : paramsStart+i])
			if authSchemeStart(part) {
				return parts
			}
			parts = append(parts, part)
			start = i + 1
		}
	}
	last := strings.TrimSpace(header[paramsStart+start:])
	if last != "" && !authSchemeStart(last) {
		parts = append(parts, last)
	}
	return parts
}

func bearerStart(header string) int {
	quoted, escaped, segmentStart := false, false, 0
	for i := 0; i < len(header); i++ {
		if !quoted && i == segmentStart {
			if start := bearerSchemeAt(header, segmentStart); start >= 0 {
				return start
			}
		}
		switch {
		case escaped:
			escaped = false
		case quoted && header[i] == '\\':
			escaped = true
		case header[i] == '"':
			quoted = !quoted
		case header[i] == ',' && !quoted:
			segmentStart = i + 1
		}
	}
	if !quoted {
		return bearerSchemeAt(header, segmentStart)
	}
	return -1
}

func bearerSchemeAt(header string, start int) int {
	for start < len(header) && (header[start] == ' ' || header[start] == '\t') {
		start++
	}
	end := start + len("Bearer")
	if end > len(header) || !strings.EqualFold(header[start:end], "Bearer") {
		return -1
	}
	if end < len(header) && header[end] != ' ' && header[end] != '\t' && header[end] != ',' {
		return -1
	}
	for end < len(header) && (header[end] == ' ' || header[end] == '\t') {
		end++
	}
	if end < len(header) && header[end] == '=' {
		return -1
	}
	return start
}

func authSchemeStart(part string) bool {
	key, _, hasValue := strings.Cut(part, "=")
	if hasValue && isAuthToken(strings.TrimSpace(key)) {
		return false
	}
	return len(strings.Fields(part)) > 0
}

func isAuthToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !isAuthTokenChar(r) {
			return false
		}
	}
	return true
}

func isAuthTokenChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
		strings.ContainsRune("!#$%&'*+-.^_`|~", r)
}

func unquoteAuthValue(value string) string {
	if len(value) > 0 && value[0] == '"' && (len(value) < 2 || value[len(value)-1] != '"') {
		return ""
	}
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return value
	}
	var result strings.Builder
	for i := 1; i < len(value)-1; i++ {
		if value[i] == '\\' {
			i++
			if i == len(value)-1 {
				return ""
			}
		}
		result.WriteByte(value[i])
	}
	return result.String()
}
