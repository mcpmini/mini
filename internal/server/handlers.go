package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/invoke"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/response"
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
	m := e.Def.ToMap()
	m["name"] = e.FullName
	m["server"] = e.Server
	m["permission"] = e.Permission
	return m, nil
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
	p.Tool = entry.ToolName.UpstreamName
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
	raw, latencyMs, toolErr := s.dispatchRaw(ctx, dispatchParams{Upstream: upstream, Tool: tool, Params: params, Session: session})
	upstream.totalLatencyMs.Add(latencyMs)
	if toolErr != nil {
		return s.handleToolErr(toolErrParams{Server: server, Tool: tool, LatencyMs: latencyMs, Err: toolErr, Session: session})
	}
	return s.buildEnvelope(envelopeParams{Entry: entry, Tool: tool, Raw: raw, Session: session, Upstream: upstream, LatencyMs: latencyMs})
}

type toolErrParams struct {
	Server    string
	Tool      string
	LatencyMs int64
	Err       error
	Session   *Session
}

func (s *Server) handleToolErr(p toolErrParams) (any, error) {
	p.Session.recordCall(p.LatencyMs, 0, true)
	s.logToolError(p.Server, p.Tool, p.LatencyMs, p.Err)
	return response.BuildError("tool_error", p.Err.Error(), false, ""), nil
}

func resolveTarget(p executeParams, entry *registry.ToolEntry) (server, tool string, params map[string]any) {
	if entry.TargetTool != "" {
		return entry.TargetServer, entry.TargetTool, mergeArgs(entry.DefaultArgs, p.Params)
	}
	return p.Server, p.Tool, p.Params
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
	Entry     *registry.ToolEntry
	Tool      string
	Raw       json.RawMessage
	Session   *Session
	Upstream  *upstreamServer
	LatencyMs int64

	// Bypass is only ever set true by proxy mode's __mini.projection:"raw".
	Bypass bool
}

func (s *Server) buildEnvelope(p envelopeParams) (any, error) {
	projCfg := s.resolveProjection(p.Entry.Server, p.Tool, p.Session)
	projStart := s.clock.Now()
	env, stats, err := s.buildProjectedEnvelope(projectedEnvelopeParams{Server: p.Entry.Server, Tool: p.Tool, Raw: p.Raw, ProjCfg: projCfg})
	if err != nil {
		return nil, err
	}
	saved := int64(stats.RawTokens - stats.SummaryTokens)
	p.Upstream.recordSaved(p.Session, p.LatencyMs, saved)
	s.logger.Debug("projection applied", "server", p.Entry.Server, "tool", p.Tool, "upstream_ms", p.LatencyMs, "proj_ms", s.clock.Since(projStart).Milliseconds(), "raw_tokens", stats.RawTokens, "tokens_saved", saved)
	return s.formatEnvelope(p.Entry.Server, p.Entry.ToolName.Name(), env, projCfg), nil
}

type projectedEnvelopeParams struct {
	Server  string
	Tool    string
	Raw     json.RawMessage
	ProjCfg *config.ProjectionConfig
	Bypass  bool
}

func (s *Server) buildProjectedEnvelope(p projectedEnvelopeParams) (*response.Envelope, response.CallStats, error) {
	return invoke.BuildEnvelope(invoke.BuildEnvelopeParams{
		Server:           p.Server,
		Tool:             p.Tool,
		Raw:              p.Raw,
		ProjCfg:          p.ProjCfg,
		ProjDefs:         s.projDefaults,
		Builder:          s.envelope,
		BypassProjection: p.Bypass,
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
