package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/version"
)

var (
	errMethodNotFound = errors.New("method not found")
	errInvalidParams  = errors.New("invalid params")
)

func (s *Server) handleRequest(
	ctx context.Context,
	req transport.Request,
	session *Session,
) (transport.Response, bool) {
	if err := transport.ValidateModernRequestMeta(req.Params); err != nil {
		return errorResponse(req.ID, transport.CodeInvalidParams, err.Error()), true
	}
	if unsupported := modernVersionError(req); unsupported != nil {
		return *unsupported, true
	}
	modern := transport.IsModernVersion(transport.ProtocolVersionFromParams(req.Params))
	if modern {
		s.activateModern(session)
	}
	if !s.awaitInitialization(ctx, req, session) {
		return errorResponse(req.ID, transport.CodeInvalidRequest, transport.NotInitializedMessage), true
	}
	s.logger.Debug("agent request", "method", req.Method, "id", req.ID)
	result, err := s.routeRequest(ctx, req, session)
	if req.Method == "initialize" && err == nil {
		session.markInitialized()
	}
	if err == nil && modern {
		result, err = addModernResultFields(req.Method, result)
	}
	return buildRouteResponse(req.ID, result, err)
}

func (s *Server) awaitInitialization(ctx context.Context, req transport.Request, session *Session) bool {
	// Initialization comes first, but ping is allowed before it:
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L118
	// A client that supports both eras probes with server/discover before it falls back to initialize:
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/8e12bf3c8f01f374146acfa19b8ece0018bfc86c/docs/specification/2026-07-28/basic/versioning.mdx#L132-L146
	if req.Method == "initialize" || req.Method == "ping" ||
		(req.Method == "server/discover" && !session.httpSession.Load()) {
		return true
	}
	// Cap the wait so stale sessions (e.g. post daemon-restart or eviction) fail fast
	// instead of blocking forever. Normal initialization is microseconds;
	// 1s is orders of magnitude more than enough.
	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return session.waitInitialized(waitCtx)
}

func buildRouteResponse(id any, result any, err error) (transport.Response, bool) {
	if err != nil {
		return routeErrorResponse(id, err), true
	}
	resp, err := okResponse(id, result)
	if err != nil {
		return errorResponse(id, transport.CodeInternalError, "marshal result: "+err.Error()), true
	}
	return resp, true
}

func routeErrorResponse(id any, err error) transport.Response {
	switch {
	case errors.Is(err, errMethodNotFound):
		return errorResponse(id, transport.CodeMethodNotFound, err.Error())
	case errors.Is(err, errInvalidParams):
		return errorResponse(id, transport.CodeInvalidParams, err.Error())
	default:
		return errorResponse(id, transport.CodeInternalError, err.Error())
	}
}

func (s *Server) routeRequest(ctx context.Context, req transport.Request, session *Session) (any, error) {
	switch req.Method {
	case "server/discover":
		if !transport.IsModernVersion(transport.ProtocolVersionFromParams(req.Params)) {
			return nil, fmt.Errorf("%w: %s", errMethodNotFound, req.Method)
		}
		return s.handleDiscover(session), nil
	case "initialize":
		return s.handleInitialize(req.Params, session)
	case "tools/list":
		return s.handleToolsList(ctx, session)
	case "tools/call":
		return s.handleToolsCall(ctx, req.Params, session)
	case "ping":
		return map[string]any{}, nil
	case transport.NotificationInitialized:
		session.markClientReady()
		return nil, nil
	case transport.NotificationCancelled:
		handleCancelled(req.Params, session)
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: %s", errMethodNotFound, req.Method)
	}
}

// handleCancelled processes a notifications/cancelled from an agent, cancelling the
// in-flight upstream call for that request ID via the session's per-request context.
// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/utilities/cancellation.mdx
func handleCancelled(params json.RawMessage, session *Session) {
	var p transport.CancelledParams
	if err := json.Unmarshal(params, &p); err != nil || len(p.RequestID) == 0 {
		return
	}
	session.cancelInFlight(p.RequestID)
	session.removeListen(p.RequestID)
}

const compactInitInstructions = "mini is an MCP proxy. Use `list` to discover tools across connected servers, `call` to invoke them, `perm_call` for tools requiring elevated permissions, and `configure` to manage servers and settings."

const proxyInitInstructions = "Results are the upstream server's own, trimmed where large: a trimmed result carries `__mini` (a key in the result object, or a final text block) with a `file` and the paths that were cut. To get cut data, call `read` with that `file` and a listed path as `filter`, or a jq filter built on it. Pass `__mini: {\"projection\": \"raw\"}` only from a script that must process the whole result without a second call. Tool arguments are the upstream server's own; `__mini` is reserved."

type initializeClientParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	ToolMode        string `json:"_mini_tool_mode"`
}

func negotiatedLegacyVersion(requested string) string {
	if transport.IsLegacyVersion(requested) {
		return requested
	}
	return transport.LatestLegacyVersion
}

// Fall back to the server-level default when the client sends no wire signal, so
// a standalone `connect --tool-mode compact` applies over HTTP too, not just stdio.
func (s *Server) resolveToolMode(signal string) transport.ToolMode {
	switch signal {
	case transport.ToolModeCompactValue:
		return transport.ToolModeCompact
	case transport.ToolModeProxyValue:
		return transport.ToolModeProxy
	default:
		if signal != "" {
			s.logger.Warn("unrecognized _mini_tool_mode signal; using server default", "signal", signal)
		}
		return s.toolMode
	}
}

func (s *Server) handleInitialize(params json.RawMessage, session *Session) (any, error) {
	var p initializeClientParams
	//nolint:errcheck // malformed params fall back to the default tool mode and the newest legacy version.
	json.Unmarshal(params, &p)
	session.setToolModeOnce(s.resolveToolMode(p.ToolMode))
	return transport.InitializeResult{
		ProtocolVersion: negotiatedLegacyVersion(p.ProtocolVersion),
		Capabilities:    map[string]any{"tools": map[string]any{"listChanged": true}},
		ServerInfo:      transport.ServerInfo{Name: "mini", Version: version.Version},
		Instructions:    s.initInstructions(session),
	}, nil
}

func (s *Server) initInstructions(session *Session) string {
	if session.toolMode() == transport.ToolModeProxy {
		return proxyInitInstructions
	}
	return compactInitInstructions
}

func (s *Server) handleToolsList(ctx context.Context, session *Session) (any, error) {
	if session.toolMode() != transport.ToolModeProxy {
		return map[string]any{"tools": s.toolSchemas}, nil
	}
	if err := s.waitForStartup(ctx); err != nil {
		return nil, err
	}
	schemas, err := buildProxyToolSchemas(s.reg.AllFull())
	if err != nil {
		return nil, err
	}
	return map[string]any{"tools": schemas}, nil
}

func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage, session *Session) (any, error) {
	call, err := parseToolCall(params)
	if err != nil {
		return nil, err
	}
	result, err := s.routeTool(ctx, call, session)
	if err != nil {
		return normalizeToolCallError(err)
	}
	return normalizeToolCallResult(result), nil
}

type toolCallRequest struct {
	Name           string                `json:"name"`
	Arguments      json.RawMessage       `json:"arguments"`
	Meta           transport.RequestMeta `json:"_meta"`
	InputResponses json.RawMessage       `json:"inputResponses"`
	RequestState   json.RawMessage       `json:"requestState"`
}

func parseToolCall(params json.RawMessage) (toolCallRequest, error) {
	var call toolCallRequest
	if err := json.Unmarshal(params, &call); err != nil {
		return toolCallRequest{}, fmt.Errorf("%w: tools/call: %w", errInvalidParams, err)
	}
	if call.Name == "" {
		return toolCallRequest{}, fmt.Errorf("%w: tools/call requires name", errInvalidParams)
	}
	return call, nil
}

// mrtr is nil for legacy agents: only a modern agent can answer an upstream's input request.
func (c toolCallRequest) mrtr() *transport.ToolCallMRTR {
	if !transport.IsModernVersion(c.Meta.ProtocolVersion) {
		return nil
	}
	mrtr := &transport.ToolCallMRTR{InputResponses: c.InputResponses, RequestState: c.RequestState}
	if caps := c.Meta.ClientCapabilities; caps != nil {
		mrtr.Elicitation = caps.Elicitation
	}
	return mrtr
}

func normalizeToolCallError(err error) (any, error) {
	if errors.Is(err, errInvalidParams) {
		return nil, err
	}
	return toolErrorResult(err.Error()), nil
}

func normalizeToolCallResult(result any) any {
	if env, ok := result.(*response.Envelope); ok && env.Error != "" {
		return toolErrorResult(mustJSON(env))
	}
	if ur, ok := result.(upstreamResult); ok {
		return ur
	}
	if fe, ok := result.(formattedEnvelope); ok {
		if fe.isError {
			return toolErrorResult(fe.text)
		}
		return toolOKResult(fe.text)
	}
	return toolOKResult(result)
}

func (s *Server) routeTool(ctx context.Context, call toolCallRequest, session *Session) (any, error) {
	if session.toolMode() == transport.ToolModeProxy {
		return s.routeProxyTool(ctx, call, session)
	}
	return s.routeStandardTool(ctx, standardToolRouteParams{
		Name: call.Name, Args: call.Arguments, Session: session,
	})
}

type standardToolRouteParams struct {
	Name    string
	Args    json.RawMessage
	Session *Session
}

func (s *Server) routeStandardTool(ctx context.Context, p standardToolRouteParams) (any, error) {
	switch p.Name {
	case "list":
		return s.handleList(ctx, p.Args)
	case "call":
		return s.handleExecute(ctx, p.Args, p.Session)
	case "perm_call":
		return s.handleExecuteProtected(ctx, p.Args, p.Session)
	case "config":
		return s.handleConfigure(ctx, p.Args, p.Session)
	default:
		return nil, fmt.Errorf("%w: unknown tool: %s", errInvalidParams, p.Name)
	}
}

func okResponse(id any, result any) (transport.Response, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return transport.Response{}, err
	}
	return transport.Response{JsonRPC: transport.JsonRPCVersion, ID: id, Result: raw}, nil
}

func errorResponse(id any, code int, msg string) transport.Response {
	return transport.Response{
		JsonRPC: transport.JsonRPCVersion,
		ID:      id,
		Error:   &transport.RPCError{Code: code, Message: msg},
	}
}

func toolOKResult(content any) map[string]any {
	var text string
	if s, ok := content.(string); ok {
		text = s
	} else {
		text = mustJSON(content)
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	}
}

func toolErrorResult(msg string) map[string]any {
	return map[string]any{
		"isError": true,
		"content": []map[string]any{{"type": "text", "text": msg}},
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Should not happen for the maps/structs we pass here; log rather than silently return "".
		slog.Default().Error("mustJSON: marshal failed", "err", err, "value", fmt.Sprintf("%T", v))
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
