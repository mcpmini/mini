package registry

import "github.com/mcpmini/mini/internal/transport"

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
	Server   string
	ToolName ToolName
	FullName string // "server.tool" using visible name (alias if set)
	Def      transport.ToolDefinition
	Hidden   bool
}
