package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/response"
	"github.com/mcpmini/mini/internal/transport"
)

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
		return fmt.Errorf("invalid server name: %q", p.Server)
	}
	if !config.ValidToolName.MatchString(p.Tool) {
		return fmt.Errorf("invalid tool name: %q", p.Tool)
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
		if s.cfg.DisableListHidden {
			return nil, fmt.Errorf("listing hidden tools is disabled by server configuration (disable_list_hidden: true)")
		}
		return s.reg.AllWithHidden(), nil
	case p.Query != "":
		return s.reg.Search(p.Query), nil
	default:
		return s.reg.All(), nil
	}
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
	var p executeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := validateExecuteParams(p); err != nil {
		return nil, err
	}
	entry, err := s.reg.Lookup(toolFullName(p.Server, p.Tool))
	if err != nil {
		return nil, err
	}
	if entry.Permission == config.PermProtected {
		return nil, fmt.Errorf("tool %q is protected — use perm_call instead", entry.FullName)
	}
	if !entry.ReadOnly && !s.hasProjectionCoverage(p.Server, p.Tool, session) {
		return nil, fmt.Errorf("tool %q has no projection configured — responses may be large or unfiltered; use perm_call to proceed, or add a projection entry (config action:set_projection)", entry.FullName)
	}
	return s.callUpstream(ctx, p, entry, session)
}

func (s *Server) handleExecuteProtected(ctx context.Context, raw json.RawMessage, session *Session) (any, error) {
	var p executeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := validateExecuteParams(p); err != nil {
		return nil, err
	}
	entry, err := s.reg.Lookup(toolFullName(p.Server, p.Tool))
	if err != nil {
		return nil, err
	}
	// Allow: explicitly protected tools, or open tools with no projection coverage.
	// The latter lets agents acknowledge the raw-response risk without a full config change.
	if entry.Permission != config.PermProtected && s.hasProjectionCoverage(p.Server, p.Tool, session) {
		return nil, fmt.Errorf("tool %q is not protected — use call instead", entry.FullName)
	}
	return s.callUpstream(ctx, p, entry, session)
}

// hasProjectionCoverage reports whether a tool has an explicit projection entry or a
// wildcard "*" for its server. Returns true when no projections file exists for the
// server at all — the restriction only kicks in once a projections file is present.
func (s *Server) hasProjectionCoverage(server, tool string, session *Session) bool {
	if session.GetProjection(toolFullName(server, tool)) != nil {
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
		session.recordCall(latencyMs, 0, true)
		return response.BuildError("tool_error", toolErr.Error(), false, ""), nil
	}
	result, err := s.buildEnvelope(server, tool, raw, session, latencyMs)
	if err != nil {
		return nil, err
	}
	if env, ok := result.(*response.Envelope); ok {
		upstream.estTokensSaved.Add(int64(env.EstimatedTokensSaved))
	}
	return result, nil
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
	var raw json.RawMessage
	var err error
	if upstream.cfg.SessionMode == config.SessionModePerSession {
		raw, err = s.callPerSession(ctx, upstream, tool, params, session)
	} else {
		raw, err = upstream.callTool(ctx, tool, params)
		if err != nil && isConnError(err) && upstream.reconnecting.CompareAndSwap(false, true) {
			go s.reconnectLoop(upstream)
		}
	}
	return raw, time.Since(start).Milliseconds(), err
}

func (s *Server) callPerSession(ctx context.Context, upstream *upstreamServer, tool string, params map[string]any, session *Session) (json.RawMessage, error) {
	conn, err := s.getOrDialSessionConn(ctx, upstream, session)
	if err != nil {
		return nil, fmt.Errorf("per_session dial: %w", err)
	}
	args, _ := json.Marshal(transport.ToolCallParams{Name: tool, Arguments: params})
	raw, err := conn.Call(ctx, "tools/call", args)
	if err != nil {
		var rpcErr *transport.RPCError
		if errors.As(err, &rpcErr) {
			return nil, err
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		session.RemoveConn(upstream.cfg.Name)
		return nil, connError{err}
	}
	result, toolErr := extractContent(raw)
	return result, toolErr
}

func (s *Server) getOrDialSessionConn(ctx context.Context, upstream *upstreamServer, session *Session) (transport.Connection, error) {
	if conn := session.GetConn(upstream.cfg.Name); conn != nil {
		return conn, nil
	}
	conn, err := session.dialOnceFor(upstream.cfg.Name, func() (transport.Connection, error) {
		return s.dialPerSessionConn(ctx, upstream, session)
	})
	if err != nil {
		return nil, err
	}
	if !s.isUpstreamRegistered(upstream.cfg.Name) {
		session.RemoveConn(upstream.cfg.Name)
		conn.Close()
		return nil, fmt.Errorf("server %q removed during dial", upstream.cfg.Name)
	}
	return conn, nil
}

func (s *Server) dialPerSessionConn(ctx context.Context, upstream *upstreamServer, session *Session) (transport.Connection, error) {
	if conn := session.GetConn(upstream.cfg.Name); conn != nil {
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

func (s *Server) buildEnvelope(server, tool string, raw json.RawMessage, session *Session, latencyMs int64) (any, error) {
	projCfg := s.resolveProjection(server, tool, session)
	env, err := s.buildProjectedEnvelope(server, tool, raw, projCfg)
	if err != nil {
		return nil, err
	}
	env.LatencyMs = latencyMs
	session.recordCall(latencyMs, int64(env.EstimatedTokensSaved), false)
	return s.formatEnvelope(server, tool, env, projCfg), nil
}

func (s *Server) buildProjectedEnvelope(server, tool string, raw json.RawMessage, projCfg *config.ProjectionConfig) (*response.Envelope, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("parse upstream response: %w", err)
	}
	result := projection.Apply(value, projCfg, s.projDefaults)
	env, _, err := s.envelope.Build(response.BuildParams{
		Server:      server,
		Tool:        tool,
		Raw:         raw,
		Summary:     result.Summary,
		Elided:      result.ElidedKeys,
		Passthrough: result.Passthrough,
	})
	return env, err
}

func (s *Server) formatEnvelope(server, tool string, env *response.Envelope, projCfg *config.ProjectionConfig) any {
	format := s.cfg.ResponseFormat
	if projCfg != nil && projCfg.Format != "" {
		format = projCfg.Format
	}
	if format == "lines" {
		return renderLines(server, tool, env)
	}
	return env
}

func (s *Server) resolveProjection(server, tool string, session *Session) *config.ProjectionConfig {
	if p := session.GetProjection(toolFullName(server, tool)); p != nil {
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
