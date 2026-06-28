package transport

import (
	"context"
	"encoding/json"
)

// ToolDefinition is a compact representation of a tool from an upstream MCP server.
// Fields follow the MCP Tool schema: https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/schema/2025-11-25/schema.json
type ToolDefinition struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema,omitempty"`
	Annotations  json.RawMessage `json:"annotations,omitempty"`
	Title        json.RawMessage `json:"title,omitempty"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Meta         json.RawMessage `json:"_meta,omitempty"` // Go identifiers cannot start with underscore
	Icons        json.RawMessage `json:"icons,omitempty"`
	Execution    json.RawMessage `json:"execution,omitempty"`
}

// Connection abstracts a connection to an upstream MCP server.
// Implementations: StdioConnection (subprocess), SSEConnection (HTTP/SSE).
type Connection interface {
	// Call invokes a JSON-RPC method on the upstream server.
	Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
	// ListTools returns the full tool list from the upstream server.
	ListTools(ctx context.Context) ([]ToolDefinition, error)
	// Health checks if the upstream server is alive.
	Health(ctx context.Context) error
	// Close shuts down the connection.
	Close() error
}
