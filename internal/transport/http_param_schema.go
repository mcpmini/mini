package transport

import (
	"encoding/json"
	"slices"
	"strings"
)

func parseHeaderParameters(raw json.RawMessage) ([]headerParameter, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, ""
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || root == nil {
		return nil, "inputSchema must be an object"
	}
	params, reason := parseSchemaProperties(root, nil)
	if reason != "" {
		return nil, reason
	}
	if schemaHasHiddenHeader(raw, true) {
		return nil, "x-mcp-header appears outside properties"
	}
	return params, ""
}

func parseSchemaProperties(schema map[string]json.RawMessage, parent []string) ([]headerParameter, string) {
	result, reason := parseDirectHeader(schema, parent)
	if reason != "" {
		return nil, reason
	}
	nested, reason := parsePropertyHeaders(schema["properties"], parent)
	if reason != "" {
		return nil, reason
	}
	result = append(result, nested...)
	if hasDuplicateHeaderNames(result) {
		return nil, "header names must be case-insensitively unique"
	}
	return result, ""
}

func parseDirectHeader(schema map[string]json.RawMessage, parent []string) ([]headerParameter, string) {
	if raw, exists := schema["x-mcp-header"]; exists {
		var name string
		if json.Unmarshal(raw, &name) != nil || !validHeaderToken(name) {
			return nil, "header name must be a non-empty HTTP token"
		}
		typeRaw, exists := schema["type"]
		var typ string
		if !exists || json.Unmarshal(typeRaw, &typ) != nil || !validHeaderType(typ) {
			return nil, "annotated property type must be string, integer, or boolean"
		}
		return []headerParameter{{name: name, path: slices.Clone(parent)}}, ""
	}
	return nil, ""
}

func parsePropertyHeaders(raw json.RawMessage, parent []string) ([]headerParameter, string) {
	var properties map[string]json.RawMessage
	if json.Unmarshal(raw, &properties) != nil {
		return nil, ""
	}
	var result []headerParameter
	for name, property := range properties {
		var child map[string]json.RawMessage
		if json.Unmarshal(property, &child) != nil || child == nil {
			continue
		}
		nested, reason := parseSchemaProperties(child, appendPath(parent, name))
		if reason != "" {
			return nil, reason
		}
		result = append(result, nested...)
	}
	return result, ""
}

func hasDuplicateHeaderNames(headers []headerParameter) bool {
	for i := range headers {
		for j := 0; j < i; j++ {
			if strings.EqualFold(headers[i].name, headers[j].name) {
				return true
			}
		}
	}
	return false
}

func validHeaderType(typ string) bool {
	return typ == "string" || typ == "integer" || typ == "boolean"
}

func appendPath(path []string, key string) []string {
	child := make([]string, 0, len(path)+1)
	child = append(child, path...)
	return append(child, key)
}

func schemaHasHiddenHeader(raw json.RawMessage, schemaRoot bool) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return false
	}
	for key, value := range object {
		if key == "x-mcp-header" && schemaRoot {
			return true
		}
		if key == "properties" {
			var children map[string]json.RawMessage
			if json.Unmarshal(value, &children) == nil {
				for _, child := range children {
					if schemaHasHiddenHeader(child, false) {
						return true
					}
				}
			}
			continue
		}
		if containsHeaderKeyword(value) {
			return true
		}
	}
	return false
}

func containsHeaderKeyword(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil && object != nil {
		if _, ok := object["x-mcp-header"]; ok {
			return true
		}
		for _, value := range object {
			if containsHeaderKeyword(value) {
				return true
			}
		}
		return false
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		for _, item := range items {
			if containsHeaderKeyword(item) {
				return true
			}
		}
	}
	return false
}

func validHeaderToken(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if !isHeaderTokenByte(ch) {
			return false
		}
	}
	return true
}

func isHeaderTokenByte(ch byte) bool {
	return ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'Z' ||
		ch >= 'a' && ch <= 'z' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(ch))
}
