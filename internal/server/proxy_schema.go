package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/mcpmini/mini/internal/registry"
)

func buildProxyToolSchemas(entries []*registry.ToolEntry) ([]map[string]any, error) {
	out := []map[string]any{miniConfigSchema(), miniReadSchema()}
	for _, e := range entries {
		schema, err := proxyUpstreamToolSchema(e)
		if err != nil {
			return nil, err
		}
		out = append(out, schema)
	}
	return out, nil
}

func proxyUpstreamToolSchema(e *registry.ToolEntry) (map[string]any, error) {
	m, err := e.Def.ToMap()
	if err != nil {
		return nil, fmt.Errorf("tool %q: %w", e.FullName, err)
	}
	m["name"] = e.Server + "__" + e.ToolName.Name()
	m["inputSchema"] = proxyInputSchema(e)
	m["outputSchema"] = proxyOutputSchema(e)
	return m, nil
}

func proxyInputSchema(e *registry.ToolEntry) map[string]any {
	args := scopedSchema(
		schemaScope{
			Raw:      e.Def.InputSchema,
			Server:   e.Server,
			Tool:     e.ToolName.Name(),
			Kind:     "input",
			Fallback: defaultObjectSchema,
		},
	)
	wrapped := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"args":   args,
			"__mini": miniInputControlSchema(),
		},
		"additionalProperties": false,
	}
	if !canOmitArgs(args) {
		wrapped["required"] = []string{"args"}
	}
	return wrapped
}

func miniInputControlSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"projection": map[string]any{
				"type": "string",
				"enum": []string{"default", "raw"},
			},
		},
		"additionalProperties": false,
	}
}

func proxyOutputSchema(e *registry.ToolEntry) map[string]any {
	data := scopedSchema(
		schemaScope{
			Raw:      e.Def.OutputSchema,
			Server:   e.Server,
			Tool:     e.ToolName.Name(),
			Kind:     "output",
			Fallback: defaultEmptySchema,
		},
	)
	return map[string]any{
		"type":     "object",
		"required": []string{"data"},
		"properties": map[string]any{
			"data":   data,
			"__mini": miniOutputMetaSchema(),
		},
	}
}

func miniOutputMetaSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"msg":         map[string]any{"type": "string"},
			"file":        map[string]any{"type": "string"},
			"excluded":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"truncated":   truncatedItemsSchema(),
			"passthrough": map[string]any{"type": "object"},
		},
	}
}

func truncatedItemsSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":     "object",
			"required": []string{"path"},
			"properties": map[string]any{
				"path":  map[string]any{"type": "string"},
				"chars": map[string]any{"type": "integer"},
				"items": map[string]any{"type": "integer"},
			},
		},
	}
}

type schemaScope struct {
	Raw      json.RawMessage
	Server   string
	Tool     string
	Kind     string
	Fallback func() map[string]any
}

// Nesting under args/data would break a client's $ref/$defs resolution unless
// anchored by a fresh $id.
func scopedSchema(s schemaScope) map[string]any {
	schema := parseSchema(s.Raw, s.Fallback)
	if _, hasID := schema["$id"]; !hasID {
		schema["$id"] = fmt.Sprintf("mini:schema:%s/%s/%s", s.Server, s.Tool, s.Kind)
	}
	return schema
}

func parseSchema(raw json.RawMessage, fallback func() map[string]any) map[string]any {
	if !json.Valid(raw) {
		return fallback()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var m map[string]any
	if err := decoder.Decode(&m); err != nil || m == nil {
		return fallback()
	}
	return m
}

func defaultObjectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func defaultEmptySchema() map[string]any {
	return map[string]any{}
}

func canOmitArgs(schema map[string]any) bool {
	if t, ok := schema["type"].(string); ok && t != "object" {
		return false
	}
	if hasNonEmptyRequired(schema) || hasPositiveMinProperties(schema) {
		return false
	}
	return !hasComposingKeyword(schema)
}

func hasNonEmptyRequired(schema map[string]any) bool {
	req, ok := schema["required"].([]any)
	return ok && len(req) > 0
}

func hasPositiveMinProperties(schema map[string]any) bool {
	switch n := schema["minProperties"].(type) {
	case json.Number:
		value, err := n.Float64()
		return value > 0 && (err == nil || errors.Is(err, strconv.ErrRange))
	case float64:
		return n > 0
	default:
		return false
	}
}

func hasComposingKeyword(schema map[string]any) bool {
	for _, key := range []string{"$ref", "allOf", "anyOf", "oneOf", "not", "if", "const", "enum"} {
		if _, ok := schema[key]; ok {
			return true
		}
	}
	return false
}
