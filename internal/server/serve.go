package server

import (
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
		line := append([]byte(nil), scanner.Bytes()...)
		if len(line) == 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if resp, send := s.handleLine(ctx, line, session); send {
				writeOut(resp)
			}
		}()
	}
	wg.Wait()
	return scanner.Err()
}

// startNotifyForwarder launches a goroutine that writes notifications from ch to out.
// Returns a writeOut function (shared by request goroutines) and a closer that must be
// called after all request goroutines have finished — it flushes remaining notifications.
func startNotifyForwarder(out io.Writer, ch chan json.RawMessage) (func(any), func()) {
	var mu sync.Mutex
	writeOut := func(v any) {
		mu.Lock()
		writeJSON(out, v) //nolint:errcheck
		mu.Unlock()
	}
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
	s.logger.Debug("agent request", "method", req.Method, "id", req.ID)
	result, err := s.dispatch(ctx, req, session)
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
		return s.handleInitialize(req.Params)
	case "tools/list":
		return s.handleToolsList()
	case "tools/call":
		return s.handleToolsCall(ctx, req.Params, session)
	case "ping":
		return map[string]any{}, nil
	case transport.NotificationInitialized:
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: %s", errMethodNotFound, req.Method)
	}
}

const initInstructions = "mini is an MCP proxy. Use `list` to discover tools across connected servers, `call` to invoke them, `perm_call` for tools requiring elevated permissions, and `configure` to manage servers and settings."

const proxyInitInstructions = "Responses are projected for efficiency. mini_read(path) for full data. mini_config for server management."

func (s *Server) handleInitialize(_ json.RawMessage) (any, error) {
	instructions := initInstructions
	if s.proxyMode {
		instructions = proxyInitInstructions
	}
	return transport.InitializeResult{
		ProtocolVersion: transport.ProtocolVersion,
		Capabilities:    map[string]any{"tools": map[string]any{}},
		ServerInfo:      transport.ServerInfo{Name: "mini", Version: transport.Version},
		Instructions:    instructions,
	}, nil
}

func (s *Server) handleToolsList() (any, error) {
	if s.proxyMode {
		return map[string]any{"tools": buildProxyToolSchemas(s.reg.AllFull())}, nil
	}
	return map[string]any{"tools": s.toolSchemas}, nil
}

func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage, session *Session) (any, error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("%w: tools/call: %w", errInvalidParams, err)
	}
	if call.Name == "" {
		return nil, fmt.Errorf("%w: tools/call requires name", errInvalidParams)
	}
	result, err := s.routeTool(ctx, call.Name, call.Arguments, session)
	if err != nil {
		if errors.Is(err, errInvalidParams) {
			return nil, err
		}
		return toolErrorResult(err.Error()), nil
	}
	if env, ok := result.(*response.Envelope); ok && env.Error != "" {
		return toolErrorResult(mustJSON(env)), nil
	}
	return toolOKResult(result), nil
}

func (s *Server) routeTool(ctx context.Context, name string, args json.RawMessage, session *Session) (any, error) {
	if s.proxyMode {
		return s.routeProxyTool(ctx, name, args, session)
	}
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
