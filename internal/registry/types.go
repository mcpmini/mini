package registry

import (
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
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
	Target        Target
	Server        string
	ToolName      ToolName
	FullName      string // "server.tool" using visible name (alias if set)
	FullNameLower string // pre-lowercased for search
	Def           transport.ToolDefinition
	DescLower     string // pre-lowercased for search
	Permission    config.PermissionLevel
}

//sumtype:decl
type Target interface {
	toolTarget()
}

type UpstreamTarget struct {
	Server string
	Tool   string
}

type ActionTarget struct {
	Server      string
	Tool        string
	DefaultArgs map[string]any
}

func (UpstreamTarget) toolTarget() {}
func (ActionTarget) toolTarget()   {}

// CompactEntry is what discover returns per tool — no full schema.
type CompactEntry struct {
	Name        string                 `json:"name"`
	Server      string                 `json:"server"`
	Description string                 `json:"description"`
	Permission  config.PermissionLevel `json:"permission"`
}

func (e *ToolEntry) Compact() CompactEntry {
	return CompactEntry{
		Name:        e.FullName,
		Server:      e.Server,
		Description: e.Def.Description,
		Permission:  e.Permission,
	}
}
