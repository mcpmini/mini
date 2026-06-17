package registry

import (
	"encoding/json"

	"github.com/mcpmini/mini/internal/config"
)

// ToolName pairs the upstream dispatch name with the agent-visible name.
// For non-aliased tools UpstreamName == Name(). For aliased tools, Alias
// holds the visible name and UpstreamName holds the real dispatch name.
type ToolName struct {
	UpstreamName string
	Alias        string
}

// Name returns the agent-visible name: the alias if set, otherwise the upstream name.
func (n ToolName) Name() string {
	if n.Alias != "" {
		return n.Alias
	}
	return n.UpstreamName
}

type ToolEntry struct {
	Server        string
	ToolName      ToolName
	FullName      string // "server.tool" using visible name (alias if set)
	FullNameLower string // pre-lowercased for search
	Description   string
	DescLower     string // pre-lowercased for search
	InputSchema   json.RawMessage
	Annotations json.RawMessage
	Permission  config.PermissionLevel

	// Virtual-tool fields (set only for actions).
	TargetServer string
	TargetTool   string
	DefaultArgs  map[string]any
}

// CompactEntry is what discover returns per tool — no full schema.
type CompactEntry struct {
	Name        string                 `json:"name"`
	Server      string                 `json:"server"`
	Description string                 `json:"description"`
	Permission config.PermissionLevel `json:"permission"`
}

func (e *ToolEntry) SchemaFields() map[string]any {
	m := map[string]any{
		"description": e.Description,
		"inputSchema": e.InputSchema,
	}
	if len(e.Annotations) > 0 {
		m["annotations"] = e.Annotations
	}
	return m
}

func (e *ToolEntry) Compact() CompactEntry {
	return CompactEntry{
		Name:        e.FullName,
		Server:      e.Server,
		Description: e.Description,
		Permission: e.Permission,
	}
}
