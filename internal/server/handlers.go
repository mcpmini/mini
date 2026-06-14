package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/transport"
)

func (s *Server) logToolError(server, tool string, latencyMs int64, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	if isConnError(err) || errors.Is(err, context.DeadlineExceeded) {
		s.logger.Warn("tool call failed", "server", server, "tool", tool, "latency_ms", latencyMs, "err", err)
		return
	}
	s.logger.Debug("tool error", "server", server, "tool", tool, "latency_ms", latencyMs, "err", err)
}

type listParams struct {
	Query  string `json:"query"`
	Tool   string `json:"tool"`
	Detail bool   `json:"detail"`
	Hidden bool   `json:"hidden"`
}

type executeParams struct {
	Server string         `json:"server"`
	Tool   string         `json:"tool"`
	Params map[string]any `json:"params"`
}

func toolFullName(server, tool string) string { return server + "." + tool }

func validateExecuteParams(p executeParams) error {
	if !config.ValidServerName.MatchString(p.Server) {
		return fmt.Errorf("%w: invalid server name: %q", errInvalidParams, p.Server)
	}
	if !config.ValidToolName.MatchString(p.Tool) {
		return fmt.Errorf("%w: invalid tool name: %q", errInvalidParams, p.Tool)
	}
	return nil
}

func validateServerName(name string) error {
	if !config.ValidServerName.MatchString(name) {
		return fmt.Errorf("invalid server name: %q", name)
	}
	return nil
}

func (s *Server) handleList(_ context.Context, raw json.RawMessage) (any, error) {
	var p listParams
	if err := unmarshalOptional(raw, &p); err != nil {
		return nil, err
	}
	switch {
	case p.Tool != "" && p.Detail:
		return s.listDetail(p.Tool)
	case p.Hidden:
		return s.listHidden()
	case p.Query != "":
		return s.reg.Search(p.Query), nil
	default:
		return s.reg.All(), nil
	}
}

func (s *Server) listHidden() (any, error) {
	if s.cfg.DisableListHidden {
		return nil, fmt.Errorf("listing hidden tools is disabled by server configuration (disable_list_hidden: true)")
	}
	return s.reg.AllWithHidden(), nil
}

func (s *Server) listDetail(fullName string) (any, error) {
	e, err := s.reg.Lookup(fullName)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"name":        e.FullName,
		"description": e.Description,
		"server":      e.Server,
		"permission":  e.Permission,
		"inputSchema": e.InputSchema,
	}, nil
}

func (s *Server) handleExecute(ctx context.Context, raw json.RawMessage, session *Session) (any, error) {
	p, entry, err := s.resolveExecute(raw)
	if err != nil {
		return toolErrorIfNotFound(err)
	}
	if entry.Permission == config.PermProtected {
		return nil, fmt.Errorf("tool %q is protected — use perm_call instead", entry.FullName)
	}
	if !s.hasProjectionCoverage(p.Server, p.Tool, session) {
		return nil, fmt.Errorf("tool %q has no projection configured — add one with config(action:set_projection) or use perm_call to invoke without projection", entry.FullName)
	}
	return s.callUpstream(ctx, p, entry, session)
}

func (s *Server) resolveExecute(raw json.RawMessage) (executeParams, *registry.ToolEntry, error) {
	var p executeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return executeParams{}, nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := validateExecuteParams(p); err != nil {
		return executeParams{}, nil, err
	}
	entry, err := s.reg.Lookup(toolFullName(p.Server, p.Tool))
	if err != nil {
		return executeParams{}, nil, errLookup{err}
	}
	if entry.UpstreamTool != "" {
		p.Tool = entry.UpstreamTool
	}
	return p, entry, nil
}

// errLookup wraps a registry lookup failure so handlers can convert it to a
// tool error (isError:true) instead of an MCP protocol error. From the agent's
// perspective, calling a non-existent tool is a recoverable tool failure, not
// a protocol fault.
type errLookup struct{ cause error }

func (e errLookup) Error() string { return e.cause.Error() }
func (e errLookup) Unwrap() error { return e.cause }

func toolErrorIfNotFound(err error) (any, error) {
	var le errLookup
	if errors.As(err, &le) {
		return response.BuildError("not_found", err.Error(), false, ""), nil
	}
	return nil, err
}

func (s *Server) handleExecuteProtected(ctx context.Context, raw json.RawMessage, session *Session) (any, error) {
	p, entry, err := s.resolveExecute(raw)
	if err != nil {
		return toolErrorIfNotFound(err)
	}
	// Open tools with no projection coverage can also use perm_call to opt into raw responses.
	if entry.Permission != config.PermProtected && s.hasProjectionCoverage(p.Server, p.Tool, session) {
		return nil, fmt.Errorf("tool %q is not protected — use call instead", entry.FullName)
	}
	return s.callUpstream(ctx, p, entry, session)
}

// hasProjectionCoverage reports whether a tool has an explicit projection entry or a
// wildcard "*" for its server. Returns true when no projections file exists for the
// server at all — the restriction only kicks in once a projections file is present.
func (s *Server) hasProjectionCoverage(server, tool string, session *Session) bool {
	if session.Projection(toolFullName(server, tool)) != nil {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	toolMap := s.projections[server]
	return len(toolMap) == 0 || toolMap[tool] != nil || toolMap["*"] != nil
}

func (s *Server) callUpstream(ctx context.Context, p executeParams, entry *registry.ToolEntry, session *Session) (any, error) {
	server, tool, params := resolveTarget(p, entry)
	upstream, err := s.getUpstream(server)
	if err != nil {
		return nil, err
	}
	raw, latencyMs, toolErr := s.dispatchRaw(ctx, upstream, tool, params, session)
	upstream.totalLatencyMs.Add(latencyMs)
	if toolErr != nil {
		return s.handleToolErr(server, tool, latencyMs, toolErr, session)
	}
	return s.buildEnvelope(envelopeParams{Server: server, Tool: tool, DisplayTool: entry.Name, Raw: raw, Session: session, Upstream: upstream, LatencyMs: latencyMs})
}

func (s *Server) handleToolErr(server, tool string, latencyMs int64, err error, session *Session) (any, error) {
	session.recordCall(latencyMs, 0, true)
	s.logToolError(server, tool, latencyMs, err)
	return response.BuildError("tool_error", err.Error(), false, ""), nil
}

func resolveTarget(p executeParams, entry *registry.ToolEntry) (server, tool string, params map[string]any) {
	if entry.TargetTool != "" {
		return entry.TargetServer, entry.TargetTool, mergeArgs(entry.DefaultArgs, p.Params)
	}
	return p.Server, p.Tool, p.Params
}

func (s *Server) dispatchRaw(ctx context.Context, upstream *upstreamServer, tool string, params map[string]any, session *Session) (json.RawMessage, int64, error) {
	ctx, cancel := applyToolTimeout(ctx, upstream.cfg.ToolTimeout)
	defer cancel()
	start := time.Now()
	raw, err := s.dispatchRawCall(ctx, upstream, tool, params, session)
	return raw, time.Since(start).Milliseconds(), err
}

func (s *Server) dispatchRawCall(ctx context.Context, upstream *upstreamServer, tool string, params map[string]any, session *Session) (json.RawMessage, error) {
	if upstream.cfg.SessionMode == config.SessionModePerSession {
		return s.callPerSession(ctx, upstream, tool, params, session)
	}
	raw, err := upstream.callTool(ctx, tool, params)
	s.maybeReconnect(upstream, err)
	return raw, err
}

func (s *Server) maybeReconnect(upstream *upstreamServer, err error) {
	if err == nil || !isConnError(err) {
		return
	}
	// Skip if upstream is already shutting down. callConn releases u.mu.RLock
	// before returning, so there is a narrow window where Close() can complete
	// reconnectWg.Wait() before this goroutine calls reconnectWg.Add(1). The
	// WaitGroup won't panic (w==0 when Wait already returned), but the goroutine
	// would run briefly after Close() returns. Checking Err() prevents that.
	if upstream.ctx.Err() != nil {
		return
	}
	if !upstream.reconnecting.CompareAndSwap(false, true) {
		return
	}
	s.spawnReconnect(upstream)
}

func (s *Server) spawnReconnect(upstream *upstreamServer) {
	s.reconnectWg.Add(1)
	go func() {
		defer s.reconnectWg.Done()
		s.reconnectLoop(upstream)
	}()
}

func (s *Server) callPerSession(ctx context.Context, upstream *upstreamServer, tool string, params map[string]any, session *Session) (json.RawMessage, error) {
	conn, err := s.getOrDialSessionConn(ctx, upstream, session)
	if err != nil {
		return nil, fmt.Errorf("per_session dial: %w", err)
	}
	args, _ := json.Marshal(transport.ToolCallParams{Name: tool, Arguments: params})
	raw, err := conn.Call(ctx, "tools/call", args)
	if err != nil {
		return nil, s.handleSessionConnErr(upstream, session, conn, err)
	}
	result, toolErr := invoke.ExtractContent(raw)
	return result, toolErr
}

func (s *Server) handleSessionConnErr(upstream *upstreamServer, session *Session, conn transport.Connection, err error) error {
	var rpcErr *transport.RPCError
	if errors.As(err, &rpcErr) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	s.logger.Warn("per-session connection error", "server", upstream.cfg.Name, "err", err)
	// EvictConn removes only if this conn is still the active one (identity
	// check), then we close it ourselves. This prevents a concurrent
	// goroutine's close from racing with our in-flight call.
	session.EvictConn(upstream.cfg.Name, conn)
	conn.Close()
	return connError{err}
}

func (s *Server) getOrDialSessionConn(ctx context.Context, upstream *upstreamServer, session *Session) (transport.Connection, error) {
	if conn := session.Conn(upstream.cfg.Name); conn != nil {
		return conn, nil
	}
	conn, err := session.dialOnceFor(upstream.cfg.Name, func() (transport.Connection, error) {
		return s.dialPerSessionConn(ctx, upstream, session)
	})
	if err != nil {
		return nil, err
	}
	return s.checkDialedConn(upstream.cfg.Name, conn, session)
}

func (s *Server) checkDialedConn(name string, conn transport.Connection, session *Session) (transport.Connection, error) {
	if s.isUpstreamRegistered(name) {
		return conn, nil
	}
	session.RemoveConn(name)
	conn.Close()
	return nil, fmt.Errorf("server %q removed during dial", name)
}

func (s *Server) dialPerSessionConn(ctx context.Context, upstream *upstreamServer, session *Session) (transport.Connection, error) {
	if conn := session.Conn(upstream.cfg.Name); conn != nil {
		return conn, nil
	}
	conn, err := dialUpstream(ctx, s.logger, s.cfg, upstream.cfg)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ListTools(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("init per_session conn: %w", err)
	}
	return session.GetOrSetConn(upstream.cfg.Name, conn), nil
}

func (s *Server) isUpstreamRegistered(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.upstreams[name]
	return ok
}

func mergeArgs(defaults, overrides map[string]any) map[string]any {
	out := make(map[string]any, len(defaults)+len(overrides))
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

type envelopeParams struct {
	Server      string
	Tool        string
	DisplayTool string // agent-visible name (alias or action), for "mini" format rendering
	Raw         json.RawMessage
	Session     *Session
	Upstream    *upstreamServer
	LatencyMs   int64
}

func (s *Server) buildEnvelope(p envelopeParams) (any, error) {
	projCfg := s.resolveProjection(p.Server, p.Tool, p.Session)
	projStart := time.Now()
	env, stats, err := s.buildProjectedEnvelope(p.Server, p.Tool, p.Raw, projCfg)
	if err != nil {
		return nil, err
	}
	saved := int64(stats.RawTokens - stats.SummaryTokens)
	p.Upstream.recordSaved(p.Session, p.LatencyMs, saved)
	s.logger.Debug("projection applied", "server", p.Server, "tool", p.Tool, "upstream_ms", p.LatencyMs, "proj_ms", time.Since(projStart).Milliseconds(), "raw_tokens", stats.RawTokens, "tokens_saved", saved)
	return s.formatEnvelope(p.Server, p.DisplayTool, env, projCfg), nil
}

func (s *Server) buildProjectedEnvelope(server, tool string, raw json.RawMessage, projCfg *config.ProjectionConfig) (*response.Envelope, response.CallStats, error) {
	return invoke.BuildEnvelope(invoke.BuildEnvelopeParams{
		Server:   server,
		Tool:     tool,
		Raw:      raw,
		ProjCfg:  projCfg,
		ProjDefs: s.projDefaults,
		Builder:  s.envelope,
	})
}

func (s *Server) formatEnvelope(server, displayTool string, env *response.Envelope, projCfg *config.ProjectionConfig) any {
	format := s.cfg.ResponseFormat
	if projCfg != nil && projCfg.Format != "" {
		format = projCfg.Format
	}
	if format == "mini" {
		return RenderLines(server, displayTool, env)
	}
	return env
}

func (s *Server) resolveProjection(server, tool string, session *Session) *config.ProjectionConfig {
	if p := session.Projection(toolFullName(server, tool)); p != nil {
		return p
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	toolMap := s.projections[server]
	if toolMap == nil {
		return nil
	}
	if p := toolMap[tool]; p != nil {
		return p
	}
	return toolMap["*"]
}

func (s *Server) getUpstream(serverName string) (*upstreamServer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.upstreams[serverName]
	if !ok {
		return nil, fmt.Errorf("server not connected: %s", serverName)
	}
	return u, nil
}

func unmarshalOptional(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	s := strings.TrimSpace(string(raw))
	if s == "null" || s == "" {
		return nil
	}
	return json.Unmarshal(raw, v)
}
