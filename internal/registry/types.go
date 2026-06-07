package registry

import (
	"encoding/json"

	"github.com/mcpmini/mini/internal/config"
)

type ToolEntry struct {
	Server        string
	Name          string
	FullName      string // "server.tool"
	FullNameLower string // pre-lowercased for search
	Description   string
	DescLower     string // pre-lowercased for search
	InputSchema   json.RawMessage
	Permission    config.PermissionLevel
	// ReadOnly is set when the upstream MCP advertised readOnlyHint:true in annotations.
	// Read-only tools are callable via call even without a projection entry.
	ReadOnly bool

	// Virtual-tool fields (set only for actions).
	TargetServer string         // real server to call
	TargetTool   string         // real tool name to call
	DefaultArgs  map[string]any // pre-baked args; call-time args win on conflict

	// Pipe is set for pipe virtual tools registered on the "user" server.
	Pipe *config.PipeConfig
}

// CompactEntry is what discover returns per tool — no full schema.
type CompactEntry struct {
	Name        string                 `json:"name"`
	Server      string                 `json:"server"`
	Description string                 `json:"description"`
	Permission  config.PermissionLevel `json:"permission"`
	ReadOnly    bool                   `json:"read_only,omitempty"`
}

func (e *ToolEntry) Compact() CompactEntry {
	return CompactEntry{
		Name:        e.FullName,
		Server:      e.Server,
		Description: e.Description,
		Permission:  e.Permission,
		ReadOnly:    e.ReadOnly,
	}
}
