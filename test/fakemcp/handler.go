//go:build integration

package main

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

type mcpHandler struct {
	tools        *ToolRegistry
	faults       *FaultRegistry
	listPageSize int
}

// dispatchResult carries the response and any raw-write override for fault injection.
type dispatchResult struct {
	response transport.Response
	rawWrite []byte // non-nil: write this instead of encoding response
	exit     bool   // true: process should exit (connection_drop fault)
}

func (h *mcpHandler) dispatch(req transport.Request) dispatchResult {
	toolName := extractToolName(req)

	if fault, ok := h.faults.Match(req.Method, toolName); ok {
		if result, handled := h.applyFault(req, fault); handled {
			return result
		}
	}

	return dispatchResult{response: h.handle(req)}
}

func (h *mcpHandler) applyFault(req transport.Request, f Fault) (dispatchResult, bool) {
	switch f.Type {
	case FaultDelay:
		time.Sleep(time.Duration(f.DelayMS) * time.Millisecond)
		return dispatchResult{}, false
	case FaultSlowInit:
		applySlowInit(req.Method, f.DelayMS)
		return dispatchResult{}, false
	case FaultHang:
		select {}
	case FaultDrop:
		return dispatchResult{exit: true}, true
	case FaultBadJSON:
		return dispatchResult{rawWrite: []byte("GARBAGE_NOT_JSON\n")}, true
	}
	return h.applyContentFault(req, f)
}

func applySlowInit(method string, delayMS int) {
	if method == "initialize" {
		time.Sleep(time.Duration(delayMS) * time.Millisecond)
	}
}

func (h *mcpHandler) applyContentFault(req transport.Request, f Fault) (dispatchResult, bool) {
	switch f.Type {
	case FaultRPCError:
		msg := faultMessage(f.Message, "injected rpc error")
		resp := transport.Response{JSONRPC: "2.0", ID: req.ID, Error: &transport.RPCError{Code: -32603, Message: msg}}
		return dispatchResult{response: resp}, true
	case FaultErrorResult:
		msg := faultMessage(f.Message, "injected tool error")
		return dispatchResult{response: respond(req.ID, errResult(msg))}, true
	case FaultOversized:
		return faultOversized(req.ID, f.SizeBytes), true
	}
	return dispatchResult{}, false
}

func faultMessage(message, fallback string) string {
	if message == "" {
		return fallback
	}
	return message
}

func faultOversized(id any, sizeBytes int) dispatchResult {
	size := sizeBytes
	if size == 0 {
		size = 1 << 20
	}
	content := strings.Repeat("x", size)
	result := transport.ToolCallResult{Content: []transport.ContentItem{{Type: "text", Text: content}}}
	return dispatchResult{response: respond(id, result)}
}

var fakeInitResult = transport.InitializeResult{
	ProtocolVersion: transport.ProtocolVersion,
	Capabilities:    map[string]any{"tools": map[string]any{}},
	ServerInfo:      transport.ServerInfo{Name: "fakemcp", Version: "0.1.0"},
}

func (h *mcpHandler) handle(req transport.Request) transport.Response {
	switch req.Method {
	case "initialize":
		return respond(req.ID, fakeInitResult)
	case "tools/list":
		return respond(req.ID, h.toolsListResult(req.Params))
	case "tools/call":
		p := parseToolCall(req.Params)
		fault, _ := h.faults.Match(req.Method, p.Name)
		return respond(req.ID, h.tools.Call(p.Name, p.Arguments, fault.SizeBytes))
	default:
		return methodNotFound(req)
	}
}

func (h *mcpHandler) toolsListResult(params json.RawMessage) transport.ToolsListResult {
	tools := h.tools.MCPTools()
	if h.listPageSize <= 0 {
		return transport.ToolsListResult{Tools: tools}
	}
	slices.SortFunc(tools, func(a, b transport.MCPTool) int {
		return strings.Compare(a.Name, b.Name)
	})
	var p struct {
		Cursor string `json:"cursor"`
	}
	json.Unmarshal(params, &p) //nolint:errcheck
	offset, _ := strconv.Atoi(p.Cursor)
	end := min(offset+h.listPageSize, len(tools))
	r := transport.ToolsListResult{Tools: tools[offset:end]}
	if end < len(tools) {
		r.NextCursor = strconv.Itoa(end)
	}
	return r
}

func parseToolCall(raw json.RawMessage) transport.ToolCallParams {
	var p transport.ToolCallParams
	json.Unmarshal(raw, &p) //nolint:errcheck
	return p
}

func methodNotFound(req transport.Request) transport.Response {
	return transport.Response{
		JSONRPC: "2.0", ID: req.ID,
		Error: &transport.RPCError{Code: transport.CodeMethodNotFound, Message: "unknown method: " + req.Method},
	}
}

func extractToolName(req transport.Request) string {
	if req.Method != "tools/call" {
		return ""
	}
	var p transport.ToolCallParams
	json.Unmarshal(req.Params, &p) //nolint:errcheck
	return p.Name
}

func respond(id any, result any) transport.Response {
	b, _ := json.Marshal(result)
	return transport.Response{JSONRPC: "2.0", ID: id, Result: b}
}
