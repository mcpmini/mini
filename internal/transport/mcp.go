// Package transport implements MCP JSON-RPC protocol types and wire format.
// MCP uses JSON-RPC 2.0 over stdio (or HTTP/SSE).
package transport

import "encoding/json"

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"` // string | number | null
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Message }

// Notification is a JSON-RPC message with no ID (fire-and-forget).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      ClientInfo     `json:"clientInfo"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      ServerInfo     `json:"serverInfo"`
	Instructions    string         `json:"instructions,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ToolsListResult struct {
	Tools      []ToolDefinition `json:"tools"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type ToolCallResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
	// StructuredContent was added in spec 2025-06-18. Servers SHOULD also include
	// a text representation in Content for backwards compatibility, but we handle
	// the case where they don't.
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-06-18/server/tools.mdx#structured-content
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
}

type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// image/audio/resource types are passed through as-is; mini only reads text items
}

const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// ProtocolVersion is the MCP spec version this implementation targets.
// The server MUST respond with this version (or another it supports); the client
// disconnects if it cannot handle the server's version.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L57
const ProtocolVersion = "2025-03-26"

// NotificationInitialized is the method name sent after the initialize handshake.
const NotificationInitialized = "notifications/initialized"

// The proxy matches on this prefix to detect lost sessions — the two must not drift apart.
const NotInitializedMessage = "not initialized: send initialize first"

// NotificationToolsChanged is sent when the available tool set changes.
const NotificationToolsChanged = "notifications/tools/list_changed"

// NotificationCancelled is sent by either party to cancel an in-progress request.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/utilities/cancellation.mdx
const NotificationCancelled = "notifications/cancelled"

const ToolModeParam = "_mini_tool_mode"

const (
	ToolModeProxyValue   = "proxy"
	ToolModeCompactValue = "compact"
)

// ToolMode selects how a session exposes upstream tools. Proxy is the zero
// value so an unconfigured session defaults to it automatically.
type ToolMode int32

const (
	ToolModeProxy   ToolMode = iota // upstream tools exposed directly as server__tool
	ToolModeCompact                 // four meta-tools: list/call/perm_call/config
)

func (m ToolMode) String() string {
	if m == ToolModeCompact {
		return "compact"
	}
	return "proxy"
}
