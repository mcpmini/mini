package server

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mcpmini/mini/internal/registry"
)

type toolInputSchema struct {
	Properties map[string]toolParamSchema `json:"properties"`
	Required   []string                   `json:"required"`
	// A bool or a sub-schema object in JSON Schema; *bool would fail to parse real schemas.
	AdditionalProperties json.RawMessage `json:"additionalProperties"`
}

type toolParamSchema struct {
	Type any `json:"type"`
}

// checkParamsAgainstSchema rejects a call the upstream would reject anyway,
// with an error naming the expected params so sandbox code can self-correct
// in one retry. Best-effort: an absent or unparseable schema never blocks a call.
func checkParamsAgainstSchema(entry *registry.ToolEntry, params map[string]any) error {
	var s toolInputSchema
	if len(entry.Def.InputSchema) == 0 || json.Unmarshal(entry.Def.InputSchema, &s) != nil || len(s.Properties) == 0 {
		return nil //nolint:nilerr // an unparseable schema must not block the call
	}
	missing := missingRequired(s.Required, params)
	unknown := unknownParams(s, params)
	if len(missing) == 0 && len(unknown) == 0 {
		return nil
	}
	return fmt.Errorf("invalid params for %q: %s; expected params: %s",
		entry.FullName, mismatchClause(missing, unknown), expectedParamsSummary(s))
}

func missingRequired(required []string, params map[string]any) []string {
	var missing []string
	for _, k := range required {
		if _, ok := params[k]; !ok {
			missing = append(missing, k)
		}
	}
	return missing
}

func unknownParams(s toolInputSchema, params map[string]any) []string {
	// JSON Schema's default allows extra keys, so only an explicit
	// additionalProperties:false makes them an error.
	if string(s.AdditionalProperties) != "false" {
		return nil
	}
	var unknown []string
	for k := range params {
		if _, ok := s.Properties[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func mismatchClause(missing, unknown []string) string {
	var parts []string
	if len(missing) > 0 {
		quoted := make([]string, len(missing))
		for i, k := range missing {
			quoted[i] = fmt.Sprintf("%q", k)
		}
		parts = append(parts, "missing required "+strings.Join(quoted, ", "))
	}
	if len(unknown) > 0 {
		quoted := make([]string, len(unknown))
		for i, k := range unknown {
			quoted[i] = fmt.Sprintf("%q", k)
		}
		parts = append(parts, "unknown "+strings.Join(quoted, ", "))
	}
	return strings.Join(parts, "; ")
}

func expectedParamsSummary(s toolInputSchema) string {
	requiredSet := make(map[string]bool, len(s.Required))
	for _, k := range s.Required {
		requiredSet[k] = true
	}
	names := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = formatParamEntry(name, s.Properties[name].Type, requiredSet[name])
	}
	return strings.Join(parts, ", ")
}

func formatParamEntry(name string, typ any, required bool) string {
	typStr := formatType(typ)
	switch {
	case typStr == "" && !required:
		return name
	case typStr == "":
		return name + " (required)"
	case !required:
		return name + " (" + typStr + ")"
	default:
		return name + " (" + typStr + ", required)"
	}
}

func formatType(typ any) string {
	switch v := typ.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "|")
	}
	return ""
}
