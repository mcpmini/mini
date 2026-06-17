package transport

import (
	"crypto/rand"
	"fmt"
)

// NewSessionID generates a random UUID v4 for session identification.
func NewSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// normalizeID converts JSON-decoded float64 IDs to int64.
// The JSON decoder unmarshals numbers into interface{} as float64,
// but RPC IDs are integers — callers compare them as int64.
func normalizeID(id any) any {
	if f, ok := id.(float64); ok {
		return int64(f)
	}
	return id
}

func toToolDefs(tools []MCPTool) []ToolDefinition {
	defs := make([]ToolDefinition, len(tools))
	for i, t := range tools {
		defs[i] = ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Annotations: t.Annotations,
		}
	}
	return defs
}
