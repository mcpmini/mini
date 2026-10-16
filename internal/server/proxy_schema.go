package server

import (
	"bytes"
	"encoding/json"
	"fmt"

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
	setProxyOutputSchema(m)
	return m, nil
}

func proxyInputSchema(e *registry.ToolEntry) map[string]any {
	schema := parseSchema(e.Def.InputSchema, defaultObjectSchema)
	if !acceptsMiniControl(schema) {
		return schema
	}
	if _, hasType := schema["type"]; !hasType {
		schema["type"] = "object"
	}
	schemaProperties(schema)["__mini"] = miniInputControlSchema()
	return schema
}

func schemaProperties(schema map[string]any) map[string]any {
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		properties = map[string]any{}
		schema["properties"] = properties
	}
	return properties
}

func acceptsMiniControl(schema map[string]any) bool {
	// Closed referenced or composed objects can reject a root-level __mini.
	for _, key := range []string{"$ref", "allOf", "anyOf", "oneOf"} {
		if _, ok := schema[key]; ok {
			return false
		}
	}
	objectType, hasType := schema["type"]
	return !hasType || objectType == "object"
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

func setProxyOutputSchema(tool map[string]any) {
	schema, ok := tool["outputSchema"].(map[string]any)
	if !ok {
		if tool["outputSchema"] == nil {
			delete(tool, "outputSchema")
		}
		return
	}
	if !isObjectSchema(schema) {
		return
	}
	properties := schemaProperties(schema)
	if _, upstreamOwnsKey := properties["__mini"]; upstreamOwnsKey {
		return
	}
	properties["__mini"] = miniOutputMetaSchema()
}

func isObjectSchema(schema map[string]any) bool {
	if schema["type"] == "object" {
		return true
	}
	_, hasProperties := schema["properties"].(map[string]any)
	return hasProperties
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
