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

func (h *mcpHandler) dispatch(req transport.Request) dispatchResult {
	if fault, ok := h.faults.Match(req.Method, extractToolName(req)); ok {
		result := applyFault(req, fault.effect)
		if _, continued := result.(continueResult); !continued {
			return result
		}
	}
	return responseResult{response: h.handle(req)}
}

func applyFault(req transport.Request, effect faultEffect) dispatchResult {
	switch f := effect.(type) {
	case delayFault:
		time.Sleep(f.duration)
		return continueResult{}
	case slowInitFault:
		if req.Method == "initialize" {
			time.Sleep(f.duration)
		}
		return continueResult{}
	case hangFault:
		select {}
	case dropFault:
		return exitResult{}
	case badJSONFault:
		return rawWriteResult{data: []byte("GARBAGE_NOT_JSON\n")}
	case rpcErrorFault:
		return responseResult{response: faultRPCError(req.ID, f.message)}
	case errorResultFault:
		return responseResult{response: respond(req.ID, errResult(f.message))}
	case oversizedFault:
		return faultOversized(req.ID, f.sizeBytes)
	}
	return continueResult{}
}

func faultRPCError(id any, message string) transport.Response {
	return transport.Response{JSONRPC: "2.0", ID: id, Error: &transport.RPCError{Code: -32603, Message: message}}
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
	return responseResult{response: respond(id, result)}
}

var fakeInitResult = transport.InitializeResult{
	ProtocolVersion: transport.LatestLegacyVersion,
	Capabilities:    map[string]any{"tools": map[string]any{"listChanged": true}},
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
		return respond(req.ID, h.tools.Call(p.Name, p.Arguments, fault.spec.SizeBytes))
	default:
		return methodNotFound(req)
	}
}

func (h *mcpHandler) toolsListResult(params json.RawMessage) transport.ToolsListResult {
	tools := h.tools.MCPTools()
	if h.listPageSize <= 0 {
		return transport.ToolsListResult{Tools: tools}
	}
	slices.SortFunc(tools, func(a, b transport.ToolDefinition) int {
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
