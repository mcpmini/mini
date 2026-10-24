package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/jq"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/transport"
)

func (s *Server) routeProxyTool(ctx context.Context, call toolCallRequest, session *Session) (any, error) {
	switch call.Name {
	case "config":
		return s.handleConfigure(ctx, call.Arguments, session)
	case "read":
		return s.handleRead(ctx, call.Arguments)
	default:
		return s.handleProxyCall(ctx, call, session)
	}
}

func (s *Server) handleProxyCall(ctx context.Context, call toolCallRequest, session *Session) (any, error) {
	server, tool, err := parseProxyToolName(call.Name)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidParams, err)
	}
	entry, err := s.lookupTool(server, tool)
	if notReady := (errServerNotReady{}); errors.As(err, &notReady) {
		return notReady.env, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidParams, err)
	}
	req, err := parseProxyRequest(call.Arguments)
	if err != nil {
		return nil, err
	}
	return s.proxyCallUpstream(ctx, proxyCallParams{
		Server:   server,
		Tool:     entry.ToolName.UpstreamName,
		Params:   req.Args,
		Entry:    entry,
		Session:  session,
		Controls: req.Controls,
		MRTR:     call.mrtr(),
	})
}

type proxyRequest struct {
	Args     map[string]any
	Controls proxyControls
}

type projectionMode string

const (
	projectionDefault projectionMode = "default"
	projectionRaw     projectionMode = "raw"
)

func (m projectionMode) Valid() bool {
	return m == "" || m == projectionDefault || m == projectionRaw
}

type proxyControls struct {
	Projection projectionMode
}

func parseProxyRequest(raw json.RawMessage) (proxyRequest, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return proxyRequest{Args: map[string]any{}}, nil
	}
	args, err := decodeProxyArgs(raw)
	if err != nil {
		return proxyRequest{}, err
	}
	controls, err := extractProxyControls(args["__mini"])
	if err != nil {
		return proxyRequest{}, err
	}
	delete(args, "__mini")
	return proxyRequest{Args: args, Controls: controls}, nil
}

func decodeProxyArgs(raw json.RawMessage) (map[string]any, error) {
	var args map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("%w: arguments must be an object: %w", errInvalidParams, err)
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

func extractProxyControls(value any) (proxyControls, error) {
	if value == nil {
		return proxyControls{}, nil
	}
	if err := rejectUnknownControls(value); err != nil {
		return proxyControls{}, err
	}
	var c struct {
		Projection projectionMode `json:"projection"`
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return proxyControls{}, fmt.Errorf("%w: __mini must be an object: %w", errInvalidParams, err)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return proxyControls{}, fmt.Errorf("%w: __mini must be an object: %w", errInvalidParams, err)
	}
	if !c.Projection.Valid() {
		return proxyControls{}, fmt.Errorf(
			"%w: __mini.projection must be \"default\" or \"raw\", got %q",
			errInvalidParams,
			c.Projection,
		)
	}
	return proxyControls{Projection: c.Projection}, nil
}

// A misspelled control would otherwise be ignored and the agent would get trimmed data it asked not to get.
func rejectUnknownControls(value any) error {
	controls, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for key := range controls {
		if key != "projection" {
			return fmt.Errorf("%w: unknown __mini key %q; the only key is \"projection\"", errInvalidParams, key)
		}
	}
	return nil
}

type proxyCallParams struct {
	Server   string
	Tool     string
	Params   map[string]any
	Entry    *registry.ToolEntry
	Session  *Session
	Controls proxyControls
	MRTR     *transport.ToolCallMRTR
}

func (s *Server) proxyCallUpstream(ctx context.Context, p proxyCallParams) (any, error) {
	upstream, err := s.getUpstream(p.Server)
	if err != nil {
		return nil, err
	}
	raw, latencyMs, toolErr := s.dispatchRaw(
		ctx,
		dispatchParams{Upstream: upstream, Tool: p.Tool, Params: p.Params, Session: p.Session, MRTR: p.MRTR},
	)
	upstream.totalLatencyMs.Add(latencyMs)
	if errors.Is(toolErr, invoke.ErrInputRequired) && p.MRTR != nil {
		p.Session.recordCall(latencyMs, 0, false)
		return upstreamResult(raw), nil
	}
	if toolErr != nil {
		p.Session.recordCall(latencyMs, 0, true)
		return proxyToolFailure(raw, toolErr), nil
	}
	return s.proxyRespond(proxyReply{Call: p, Upstream: upstream, Raw: raw, LatencyMs: latencyMs})
}

func (s *Server) proxyRespond(reply proxyReply) (any, error) {
	reply.ProjCfg = s.proxyProjectionConfig(reply.Call)
	out, err := s.proxyResultBytes(reply)
	if err != nil {
		return nil, err
	}
	saved := int64(response.EstimateTokensRaw(reply.Raw) - response.EstimateTokensRaw(out))
	reply.Upstream.recordSaved(reply.Call.Session, reply.LatencyMs, saved)
	return upstreamResult(out), nil
}

type proxyReply struct {
	Call      proxyCallParams
	Upstream  *upstreamServer
	Raw       json.RawMessage
	LatencyMs int64
	ProjCfg   *config.ProjectionConfig
}

func proxyToolFailure(raw json.RawMessage, toolErr error) any {
	if errors.Is(toolErr, invoke.ErrToolReturnedError) {
		return upstreamResult(raw)
	}
	return response.BuildError(response.BuildErrorParams{Code: "tool_error", Message: toolErr.Error()})
}

func (s *Server) proxyProjectionConfig(p proxyCallParams) *config.ProjectionConfig {
	if p.Controls.Projection == projectionRaw {
		return nil
	}
	return s.resolveProjection(p.Entry.Server, p.Tool, p.Session)
}

func (s *Server) proxyResultBytes(r proxyReply) (json.RawMessage, error) {
	if r.Call.Controls.Projection == projectionRaw {
		return r.Raw, nil
	}
	built, err := s.buildProxyResult(proxyResultParams{Raw: r.Raw, ProjCfg: r.ProjCfg})
	if err != nil {
		return nil, err
	}
	if !s.usesToon(r.ProjCfg) {
		return built.Raw, nil
	}
	return s.toonifyProxyResult(toonParams{Server: r.Call.Server, Tool: r.Call.Tool, Built: built})
}

func (s *Server) usesToon(projCfg *config.ProjectionConfig) bool {
	return config.EffectiveFormat("", config.ProjectionFormat(projCfg), s.cfg.ResponseFormat) == config.FormatToon
}

func (s *Server) handleRead(ctx context.Context, raw json.RawMessage) (any, error) {
	file, filter, err := parseReadArgs(raw)
	if err != nil {
		return nil, err
	}
	file = s.resolveReadPath(file)
	if validationErr := s.validateStorePath(file); validationErr != nil {
		return nil, validationErr
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("%w: response file not found or unreadable", errInvalidParams)
	}
	if filter == "" {
		return string(b), nil
	}
	out, err := jq.Eval(ctx, b, filter)
	if err != nil {
		return nil, fmt.Errorf("%w: read filter: %w", errInvalidParams, err)
	}
	return out, nil
}

func (s *Server) resolveReadPath(file string) string {
	if filepath.Ext(file) == "" {
		file = file + ".json"
	}
	return filepath.Join(s.store.Dir(), file)
}

func parseReadArgs(raw json.RawMessage) (file, filter string, err error) {
	var p struct {
		File   string `json:"file"`
		Filter string `json:"filter"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", "", fmt.Errorf("%w: read: %w", errInvalidParams, err)
	}
	if p.File == "" {
		return "", "", fmt.Errorf("%w: read: file is required", errInvalidParams)
	}
	return p.File, p.Filter, nil
}

func (s *Server) validateStorePath(path string) error {
	return validateStorePathWith(validateStorePathParams{
		StoreDir: s.store.Dir(),
		Path:     path,
		Resolve:  resolveSymlinks,
	})
}

type validateStorePathParams struct {
	StoreDir string
	Path     string
	Resolve  func(string) (string, error)
}

func validateStorePathWith(p validateStorePathParams) error {
	// EvalSymlinks on both sides prevents a symlink inside the store from escaping
	// confinement; on macOS, TempDir itself is a symlink (/var/... → /private/var/...).
	storeDir, err := p.Resolve(p.StoreDir)
	if err != nil {
		return fmt.Errorf("%w: read: resolve response directory: %w", errInvalidParams, err)
	}
	abs, err := p.Resolve(p.Path)
	if err != nil {
		return fmt.Errorf("%w: read: resolve response path: %w", errInvalidParams, err)
	}
	if !strings.HasPrefix(abs, storeDir+string(filepath.Separator)) {
		return fmt.Errorf("%w: read: path must be within mini response directory", errInvalidParams)
	}
	return nil
}

func resolveSymlinks(path string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	return filepath.Abs(path)
}

func parseProxyToolName(name string) (server, tool string, err error) {
	idx := strings.Index(name, "__")
	if idx < 0 {
		return "", "", fmt.Errorf("unknown proxy tool: %q (expected server__tool format)", name)
	}
	s, t := name[:idx], name[idx+2:]
	if s == "" || t == "" {
		return "", "", fmt.Errorf("invalid proxy tool name %q: server and tool must both be non-empty", name)
	}
	return s, t, nil
}

func (s *Server) resolveProjection(server, tool string, session *Session) *config.ProjectionConfig {
	if p := session.Projection(toolFullName(server, tool)); p != nil {
		return p
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	toolMap := s.projections[server]
	if toolMap == nil {
		return nil
	}
	if p := toolMap[tool]; p != nil {
		return p
	}
	return toolMap["*"]
}
