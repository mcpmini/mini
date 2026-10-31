package server

// Clients validate structuredContent against outputSchema, and trimmed results can break these.
var trimSensitiveKeywords = []string{
	"required", "dependentRequired", "minProperties",
	"minLength", "pattern", "format",
	"minItems", "maxItems", "contains", "minContains",
	"unevaluatedItems", // items "contains" marked as evaluated would otherwise be rejected
}

// The walk never enters "not" or "if": loosening what they test would tighten the schema.
var subschemaKeys = []string{
	"anyOf", "allOf", "prefixItems", "items", "additionalProperties",
	"then", "else", "propertyNames", "unevaluatedProperties",
}

var subschemaMapKeys = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"}

var arrayItemKeys = []string{"items", "prefixItems"}

func setProxyOutputSchema(tool map[string]any) {
	schema, ok := tool["outputSchema"].(map[string]any)
	if !ok {
		if tool["outputSchema"] == nil {
			delete(tool, "outputSchema")
		}
		return
	}
	if negationRefersOut(schema) {
		tool["outputSchema"] = looseOutputSchema(schema)
		return
	}
	loosenSchema(schema)
	if !allowMiniNotice(schema) {
		tool["outputSchema"] = looseOutputSchema(schema)
	}
}

func negationRefersOut(node any) bool {
	switch v := node.(type) {
	case []any:
		for _, item := range v {
			if negationRefersOut(item) {
				return true
			}
		}
	case map[string]any:
		for key, sub := range v {
			if (key == "not" || key == "if") && containsRef(sub) || negationRefersOut(sub) {
				return true
			}
		}
	}
	return false
}

func containsRef(node any) bool {
	switch v := node.(type) {
	case []any:
		for _, item := range v {
			if containsRef(item) {
				return true
			}
		}
	case map[string]any:
		if _, refers := v["$ref"]; refers {
			return true
		}
		for _, sub := range v {
			if containsRef(sub) {
				return true
			}
		}
	}
	return false
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
	if _, refers := schema["$ref"]; refers {
		return false
	}
	objectish, arrayish := isObjectish(schema), isArrayish(schema)
	if !objectish && !arrayish {
		return false
	}
	if objectish {
		allowMiniProperty(schema)
	}
	if arrayish && !allowMiniItem(schema) {
		return false
	}
	return allowMiniInBranches(schema)
}

func allowMiniProperty(schema map[string]any) {
	properties := schemaProperties(schema)
	if _, upstreamOwnsKey := properties["__mini"]; !upstreamOwnsKey {
		properties["__mini"] = miniOutputMetaSchema()
	}
}

func allowMiniInBranches(schema map[string]any) bool {
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
	if !ok || !isObjectish(schema) && !isArrayish(schema) {
		return true
	}
	return allowMiniNotice(schema)
}

func allowMiniItem(schema map[string]any) bool {
	if _, positional := schema["prefixItems"]; positional {
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

func isArrayish(schema map[string]any) bool {
	_, typed := schema["type"]
	return typeIncludes(schema, "array") || !typed && hasAnyKey(schema, arrayItemKeys)
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
