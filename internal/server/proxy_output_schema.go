package server

// Clients validate structuredContent against outputSchema, and trimmed results can break these.
var trimSensitiveKeywords = []string{
	"required", "dependentRequired", "minProperties",
	"minLength", "pattern", "format",
	"minItems", "maxItems", "contains", "minContains",
}

var subschemaKeys = []string{
	"anyOf", "allOf", "prefixItems", "items", "additionalProperties",
	"then", "else", "propertyNames", "unevaluatedProperties", "unevaluatedItems",
}

var subschemaMapKeys = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"}

var opaqueSchemaKeys = []string{"$ref", "not", "if"}

func setProxyOutputSchema(tool map[string]any) {
	schema, ok := tool["outputSchema"].(map[string]any)
	if !ok {
		if tool["outputSchema"] == nil {
			delete(tool, "outputSchema")
		}
		return
	}
	loosenSchema(schema)
	if !allowMiniNotice(schema) {
		tool["outputSchema"] = looseOutputSchema(schema)
	}
}

func loosenSchema(node any) {
	switch v := node.(type) {
	case []any:
		for _, item := range v {
			loosenSchema(item)
		}
	case map[string]any:
		loosenSchemaObject(v)
	}
}

func loosenSchemaObject(schema map[string]any) {
	for _, key := range trimSensitiveKeywords {
		delete(schema, key)
	}
	oneOfToAnyOf(schema)
	for _, key := range subschemaKeys {
		loosenSchema(schema[key])
	}
	for _, key := range subschemaMapKeys {
		named, _ := schema[key].(map[string]any)
		for _, sub := range named {
			loosenSchema(sub)
		}
	}
}

func oneOfToAnyOf(schema map[string]any) {
	branches, ok := schema["oneOf"]
	if !ok {
		return
	}
	delete(schema, "oneOf") // Without required fields, a result can match more than one branch, which oneOf rejects.
	if _, taken := schema["anyOf"]; !taken {
		schema["anyOf"] = branches
		return
	}
	allOf, _ := schema["allOf"].([]any)
	schema["allOf"] = append(allOf, map[string]any{"anyOf": branches})
}

func allowMiniNotice(schema map[string]any) bool {
	if hasAnyKey(schema, opaqueSchemaKeys) {
		return false
	}
	allowed := false
	if isObjectish(schema) {
		if !allowMiniProperty(schema) {
			return false
		}
		allowed = true
	}
	if typeIncludes(schema, "array") {
		if !allowMiniItem(schema) {
			return false
		}
		allowed = true
	}
	return allowed
}

func allowMiniProperty(schema map[string]any) bool {
	properties := schemaProperties(schema)
	if _, upstreamOwnsKey := properties["__mini"]; !upstreamOwnsKey {
		properties["__mini"] = miniOutputMetaSchema()
	}
	for _, key := range []string{"anyOf", "allOf"} {
		branches, _ := schema[key].([]any)
		for _, branch := range branches {
			if !allowMiniInBranch(branch) {
				return false
			}
		}
	}
	return true
}

func allowMiniInBranch(branch any) bool {
	schema, ok := branch.(map[string]any)
	if !ok {
		return true
	}
	if hasAnyKey(schema, opaqueSchemaKeys) {
		return false
	}
	return !isObjectish(schema) || allowMiniProperty(schema)
}

func allowMiniItem(schema map[string]any) bool {
	if hasAnyKey(schema, []string{"unevaluatedItems", "prefixItems", "allOf", "anyOf"}) {
		return false
	}
	items, hasItems := schema["items"]
	if !hasItems || items == true {
		return true
	}
	if _, isSchema := items.(map[string]any); !isSchema {
		return false
	}
	schema["items"] = map[string]any{"anyOf": []any{items, miniNoticeItemSchema()}}
	return true
}

func looseOutputSchema(schema map[string]any) map[string]any {
	if rootType, typed := schema["type"]; typed {
		return map[string]any{"type": rootType}
	}
	return map[string]any{}
}

func isObjectish(schema map[string]any) bool {
	_, typed := schema["type"]
	return !typed || typeIncludes(schema, "object")
}

func typeIncludes(schema map[string]any, name string) bool {
	switch t := schema["type"].(type) {
	case string:
		return t == name
	case []any:
		for _, v := range t {
			if v == name {
				return true
			}
		}
	}
	return false
}

func hasAnyKey(schema map[string]any, keys []string) bool {
	for _, key := range keys {
		if _, ok := schema[key]; ok {
			return true
		}
	}
	return false
}

func miniNoticeItemSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"__mini": miniOutputMetaSchema()},
		"required":   []string{"__mini"},
	}
}

func miniOutputMetaSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"msg":       map[string]any{"type": "string"},
			"file":      map[string]any{"type": "string"},
			"excluded":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"truncated": truncatedItemsSchema(),
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
