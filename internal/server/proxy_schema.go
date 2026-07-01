package server

import (
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/registry"
)

func buildProxyToolSchemas(entries []*registry.ToolEntry) []map[string]any {
	out := []map[string]any{miniConfigSchema(), miniReadSchema()}
	for _, e := range entries {
		out = append(out, proxyUpstreamToolSchema(e))
	}
	return out
}

func proxyUpstreamToolSchema(e *registry.ToolEntry) map[string]any {
	m := e.Def.ToMap()
	m["name"] = e.Server + "__" + e.ToolName.Name()
	m["inputSchema"] = proxyInputSchema(e)
	m["outputSchema"] = proxyOutputSchema(e)
	return m
}

func proxyInputSchema(e *registry.ToolEntry) map[string]any {
	args := scopedSchema(e.Def.InputSchema, e.Server, e.ToolName.Name(), "input", defaultObjectSchema)
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
	data := scopedSchema(e.Def.OutputSchema, e.Server, e.ToolName.Name(), "output", defaultEmptySchema)
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

// scopedSchema gives the schema an absolute $id so its internal $ref/$defs
// keep resolving correctly once nested under "args" or "data".
func scopedSchema(raw json.RawMessage, server, tool, kind string, fallback func() map[string]any) map[string]any {
	schema := parseSchema(raw, fallback)
	if _, hasID := schema["$id"]; !hasID {
		schema["$id"] = fmt.Sprintf("mini:schema:%s/%s/%s", server, tool, kind)
	}
	return schema
}

func parseSchema(raw json.RawMessage, fallback func() map[string]any) map[string]any {
	if len(raw) == 0 {
		return fallback()
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
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
	n, ok := schema["minProperties"].(float64)
	return ok && n > 0
}

func hasComposingKeyword(schema map[string]any) bool {
	for _, key := range []string{"$ref", "allOf", "anyOf", "oneOf", "not", "if", "const", "enum"} {
		if _, ok := schema[key]; ok {
			return true
		}
	}
	return false
}
