package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/transport"
)

// Serve dispatches each request in its own goroutine so slow upstream calls
// do not block subsequent requests from the same agent.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sessionID := transport.NewSessionID()
	session := s.sessions.getOrCreate(sessionID)
	session.proxyMode.Store(s.proxyMode) // inherit server-level mode for standalone runs
	defer s.sessions.delete(sessionID)
	return s.serveLoop(ctx, in, out, session)
}

func (s *Server) serveLoop(ctx context.Context, in io.Reader, out io.Writer, session *Session) error {
	notifyCh := session.enableNotifications()
	writeOut, closeNotify := startNotifyForwarder(out, notifyCh)
	defer closeNotify()                  // runs second: closes channel, drains forwarder
	defer session.disableNotifications() // runs first: nil field before channel closes
	var wg sync.WaitGroup
	scanner := transport.NewScanner(in)
	for scanner.Scan() {
		s.handleScannedLine(handleScannedLineParams{ctx: ctx, rawLine: scanner.Bytes(), session: session, writeOut: writeOut, wg: &wg})
	}
	// Signal any goroutines waiting for initialization that no more messages are coming.
	// This unblocks them so they can return an error and allow wg.Wait() to complete.
	session.markAborted()
	wg.Wait()
	return scanner.Err()
}

type handleScannedLineParams struct {
	ctx      context.Context
	rawLine  []byte
	session  *Session
	writeOut func(any)
	wg       *sync.WaitGroup
}

func (s *Server) handleScannedLine(p handleScannedLineParams) {
	if len(p.rawLine) == 0 {
		return
	}
	line := bytes.Clone(p.rawLine)
	// Initialize is processed synchronously (blocking the scanner loop) so that
	// session.initDone is closed before any subsequent request goroutine starts.
	// This eliminates the race between markInitialized and goroutines that wait
	// on it. The spec also says the initialize request MUST NOT be cancelled, so
	// skipping in-flight registration is correct.
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/utilities/cancellation.mdx#L32
	if peekMethod(line) == "initialize" {
		if resp, send := s.handleLine(p.ctx, line, p.session); send {
			p.writeOut(resp)
		}
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		s.dispatchWithCancel(p.ctx, line, p.session, p.writeOut)
	}()
}

func (s *Server) dispatchWithCancel(ctx context.Context, line []byte, session *Session, writeOut func(any)) {
	rawID := peekRequestID(line)
	if len(rawID) > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		session.registerInFlight(rawID, cancel)
		defer session.removeInFlight(rawID)
		defer cancel()
	}
	if resp, send := s.handleLine(ctx, line, session); send {
		writeOut(resp)
	}
}

func peekMethod(line []byte) string {
	var peek struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(line, &peek); err != nil {
		return ""
	}
	return peek.Method
}

// peekRequestID extracts the raw JSON "id" field from a JSON-RPC line without
// fully parsing it. Returns nil for notifications (no id) or parse failures.
func peekRequestID(line []byte) json.RawMessage {
	var peek struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(line, &peek); err != nil {
		return nil
	}
	if len(peek.ID) == 0 || string(peek.ID) == "null" {
		return nil
	}
	return peek.ID
}

// startNotifyForwarder launches a goroutine that writes notifications from ch to out.
// Returns a writeOut function (shared by request goroutines) and a closer that must be
// called after all request goroutines have finished — it flushes remaining notifications.
func startNotifyForwarder(out io.Writer, ch chan json.RawMessage) (func(any), func()) {
	writeOut := newSerializedWriter(out)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := range ch {
			writeOut(n)
		}
	}()
	return writeOut, func() {
		close(ch)
		wg.Wait()
	}
}

func newSerializedWriter(out io.Writer) func(any) {
	var mu sync.Mutex
	return func(v any) {
		mu.Lock()
		writeJSON(out, v) //nolint:errcheck
		mu.Unlock()
	}
}

var (
	errMethodNotFound = errors.New("method not found")
	errInvalidParams  = errors.New("invalid params")
)

func (s *Server) handleLine(ctx context.Context, line []byte, session *Session) (transport.Response, bool) {
	session.touch()
	var req transport.Request
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResponse(nil, transport.CodeParseError, "parse error"), true
	}
	if req.JSONRPC != "2.0" {
		return errorResponse(req.ID, transport.CodeInvalidRequest, "jsonrpc must be \"2.0\""), true
	}
	if req.ID == nil {
		s.dispatch(ctx, req, session) //nolint:errcheck
		return transport.Response{}, false
	}
	return s.handleRequest(ctx, req, session)
}

func (s *Server) handleRequest(ctx context.Context, req transport.Request, session *Session) (transport.Response, bool) {
	// Spec: "The initialization phase MUST be the first interaction between client and server."
	// Non-initialize, non-ping requests wait until initialize completes (or the connection closes).
	// Ping is explicitly allowed before initialization per lifecycle spec.
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L118
	if req.Method != "initialize" && req.Method != "ping" {
		if !session.waitInitialized(ctx) {
			return errorResponse(req.ID, transport.CodeInvalidRequest, "not initialized: send initialize first"), true
		}
	}
	s.logger.Debug("agent request", "method", req.Method, "id", req.ID)
	result, err := s.dispatch(ctx, req, session)
	if req.Method == "initialize" && err == nil {
		session.markInitialized()
	}
	if err != nil {
		return dispatchErrorResponse(req.ID, err), true
	}
	resp, err := okResponse(req.ID, result)
	if err != nil {
		return errorResponse(req.ID, transport.CodeInternalError, "marshal result: "+err.Error()), true
	}
	return resp, true
}

func dispatchErrorResponse(id any, err error) transport.Response {
	switch {
	case errors.Is(err, errMethodNotFound):
		return errorResponse(id, transport.CodeMethodNotFound, err.Error())
	case errors.Is(err, errInvalidParams):
		return errorResponse(id, transport.CodeInvalidParams, err.Error())
	default:
		return errorResponse(id, transport.CodeInternalError, err.Error())
	}
}

func (s *Server) dispatch(ctx context.Context, req transport.Request, session *Session) (any, error) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.Params, session)
	case "tools/list":
		return s.handleToolsList(session)
	case "tools/call":
		return s.handleToolsCall(ctx, req.Params, session)
	case "ping":
		return map[string]any{}, nil
	case transport.NotificationInitialized:
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
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(params, &p); err != nil || len(p.RequestID) == 0 {
		return
	}
	session.cancelInFlight(p.RequestID)
}

const initInstructions = "mini is an MCP proxy. Use `list` to discover tools across connected servers, `call` to invoke them, `perm_call` for tools requiring elevated permissions, and `configure` to manage servers and settings."

const proxyInitInstructions = "Responses are projected for efficiency. read(path) for full data. config for server management."

type initializeClientParams struct {
	MiniProxyMode bool `json:"_mini_proxy_mode"`
}

func (s *Server) handleInitialize(params json.RawMessage, session *Session) (any, error) {
	var p initializeClientParams
	json.Unmarshal(params, &p) //nolint:errcheck // best-effort; standard clients omit this field
	if p.MiniProxyMode {
		session.proxyMode.Store(true)
	}
	instructions := initInstructions
	if session.proxyMode.Load() {
		instructions = proxyInitInstructions
	}
	return transport.InitializeResult{
		ProtocolVersion: transport.ProtocolVersion,
		Capabilities:    map[string]any{"tools": map[string]any{"listChanged": true}},
		ServerInfo:      transport.ServerInfo{Name: "mini", Version: transport.Version},
		Instructions:    instructions,
	}, nil
}

func (s *Server) handleToolsList(session *Session) (any, error) {
	if session.proxyMode.Load() {
		return map[string]any{"tools": buildProxyToolSchemas(s.reg.AllFull())}, nil
	}
	return map[string]any{"tools": s.toolSchemas}, nil
}

func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage, session *Session) (any, error) {
	call, err := parseToolCall(params)
	if err != nil {
		return nil, err
	}
	result, err := s.routeTool(ctx, call.Name, call.Arguments, session)
	if err != nil {
		return normalizeToolCallError(err)
	}
	return normalizeToolCallResult(result), nil
}

type toolCallRequest struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
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
	return toolOKResult(result)
}

func (s *Server) routeTool(ctx context.Context, name string, args json.RawMessage, session *Session) (any, error) {
	if session.proxyMode.Load() {
		return s.routeProxyTool(ctx, name, args, session)
	}
	return s.routeStandardTool(ctx, name, args, session)
}

func (s *Server) routeStandardTool(ctx context.Context, name string, args json.RawMessage, session *Session) (any, error) {
	switch name {
	case "list":
		return s.handleList(ctx, args)
	case "call":
		return s.handleExecute(ctx, args, session)
	case "perm_call":
		return s.handleExecuteProtected(ctx, args, session)
	case "config":
		return s.handleConfigure(ctx, args, session)
	default:
		return nil, fmt.Errorf("%w: unknown tool: %s", errInvalidParams, name)
	}
}

func okResponse(id any, result any) (transport.Response, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return transport.Response{}, err
	}
	return transport.Response{JSONRPC: "2.0", ID: id, Result: raw}, nil
}

func errorResponse(id any, code int, msg string) transport.Response {
	return transport.Response{
		JSONRPC: "2.0",
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

func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", b)
	return err
}
