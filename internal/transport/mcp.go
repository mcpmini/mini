// Package transport implements MCP JSON-RPC protocol types and wire format.
// MCP uses JSON-RPC 2.0 over stdio (or HTTP/SSE).
package transport

import (
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/version"
)

const JsonRPCVersion = "2.0"

type Request struct {
	JsonRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"` // string | number | null
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JsonRPC string          `json:"jsonrpc"`
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
	JsonRPC string          `json:"jsonrpc"`
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
	TTLMs      json.RawMessage  `json:"ttlMs,omitempty"`
}

type ToolCallParams struct {
	Name           string          `json:"name"`
	Arguments      map[string]any  `json:"arguments"`
	Meta           *RequestMeta    `json:"_meta,omitempty"`
	InputResponses json.RawMessage `json:"inputResponses,omitempty"`
	RequestState   json.RawMessage `json:"requestState,omitempty"`
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

	CodeHeaderMismatch                  = -32020
	CodeMissingRequiredClientCapability = -32021
	CodeUnsupportedProtocolVersion      = -32022
)

// NotificationInitialized is the method name sent after the initialize handshake.
const NotificationInitialized = "notifications/initialized"

// The proxy matches on this prefix to detect lost sessions — the two must not drift apart.
const NotInitializedMessage = "not initialized: send initialize first"

// NotificationToolsChanged is sent when the available tool set changes.
const NotificationToolsChanged = "notifications/tools/list_changed"

// NotificationCancelled is sent by either party to cancel an in-progress request.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/utilities/cancellation.mdx
const NotificationCancelled = "notifications/cancelled"

type CancelledParams struct {
	RequestID json.RawMessage `json:"requestId"`
}

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

func newInitializeParams() InitializeParams {
	return InitializeParams{
		ProtocolVersion: LatestLegacyVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      ClientInfo{Name: "mini", Version: version.Version},
	}
}

func parseInitializeResult(raw json.RawMessage) (InitializeResult, error) {
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return InitializeResult{}, fmt.Errorf("parse initialize: %w", err)
	}
	if !IsLegacyVersion(result.ProtocolVersion) {
		return InitializeResult{}, fmt.Errorf(
			"upstream negotiated unsupported protocol version %q",
			result.ProtocolVersion,
		)
	}
	return result, nil
}
