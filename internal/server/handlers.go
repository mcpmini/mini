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

type toolErrorLogParams struct {
	Server    string
	Tool      string
	LatencyMs int64
	Err       error
}

func (s *Server) logToolError(p toolErrorLogParams) {
	if errors.Is(p.Err, context.Canceled) {
		return
	}
	if isConnError(p.Err) || errors.Is(p.Err, context.DeadlineExceeded) {
		s.logger.Warn("tool call failed", "server", p.Server, "tool", p.Tool, "latency_ms", p.LatencyMs, "err", p.Err)
		return
	}
	s.logger.Debug("tool error", "server", p.Server, "tool", p.Tool, "latency_ms", p.LatencyMs, "err", p.Err)
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

func (s *Server) handleList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p listParams
	if err := unmarshalOptional(raw, &p); err != nil {
		return nil, err
	}
	if p.Tool != "" && p.Detail {
		return s.listDetail(p.Tool)
	}
	if err := s.waitForStartup(ctx); err != nil {
		return nil, err
	}
	switch {
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
	server, tool, _ := strings.Cut(fullName, ".")
	e, err := s.lookupTool(server, tool)
	if err != nil {
		return s.toolNotFoundError(err, server, tool)
	}
	m, err := e.Def.ToMap()
	if err != nil {
		return nil, fmt.Errorf("tool %q: %w", e.FullName, err)
	}
	m["name"] = e.FullName
	m["server"] = e.Server
	m["permission"] = e.Permission
	return m, nil
}

func (s *Server) handleExecute(ctx context.Context, raw json.RawMessage, session *Session) (any, error) {
	p, entry, err := s.resolveExecute(raw)
	if err != nil {
		return s.toolNotFoundError(err, p.Server, p.Tool)
	}
	if entry.Permission == config.PermProtected {
		return nil, fmt.Errorf("tool %q is protected — use perm_call instead", entry.FullName)
	}
	if !s.hasProjectionCoverage(p.Server, p.Tool, session) {
		return nil, fmt.Errorf(
			"tool %q has no projection configured — add one with config(action:set_projection) or use perm_call to invoke without projection",
			entry.FullName,
		)
	}
	return s.callUpstream(ctx, callUpstreamParams{Execute: p, Entry: entry, Session: session})
}

func (s *Server) resolveExecute(raw json.RawMessage) (executeParams, *registry.ToolEntry, error) {
	var p executeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return executeParams{}, nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := validateExecuteParams(p); err != nil {
		return executeParams{}, nil, err
	}
	entry, err := s.lookupTool(p.Server, p.Tool)
	if err != nil {
		// Return p so callers can use server/tool for error formatting.
		return p, nil, err
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

func (s *Server) toolNotFoundError(err error, server, tool string) (any, error) {
	var notReady errServerNotReady
	var le errLookup
	var env *response.Envelope
	switch {
	case errors.As(err, &notReady):
		env = notReady.env
	case errors.As(err, &le):
		env = response.BuildError("not_found", err.Error(), false, "")
	default:
		return nil, err
	}
	return s.formatEnvelope(formatEnvelopeParams{Server: server, Tool: tool, Envelope: env})
}

func (s *Server) handleExecuteProtected(ctx context.Context, raw json.RawMessage, session *Session) (any, error) {
	p, entry, err := s.resolveExecute(raw)
	if err != nil {
		return s.toolNotFoundError(err, p.Server, p.Tool)
	}
	// Open tools with no projection coverage can also use perm_call to opt into raw responses.
	if entry.Permission != config.PermProtected && s.hasProjectionCoverage(p.Server, p.Tool, session) {
		return nil, fmt.Errorf("tool %q is not protected — use call instead", entry.FullName)
	}
	return s.callUpstream(ctx, callUpstreamParams{Execute: p, Entry: entry, Session: session})
}

// hasProjectionCoverage reports whether a tool has an explicit projection entry or a
// wildcard "*" for its server. Returns true when the server has no projections at all —
// the restriction only kicks in once it has some.
func (s *Server) hasProjectionCoverage(server, tool string, session *Session) bool {
	if session.Projection(toolFullName(server, tool)) != nil {
		return true
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	toolMap := s.projections[server]
	return len(toolMap) == 0 || toolMap[tool] != nil || toolMap["*"] != nil
}

type callUpstreamParams struct {
	Execute executeParams
	Entry   *registry.ToolEntry
	Session *Session
}

func (s *Server) callUpstream(ctx context.Context, p callUpstreamParams) (any, error) {
	upstream, err := s.getUpstream(p.Execute.Server)
	if err != nil {
		return nil, err
	}
	raw, latencyMs, toolErr := s.dispatchRaw(
		ctx,
		dispatchParams{Upstream: upstream, Tool: p.Execute.Tool, Params: p.Execute.Params, Session: p.Session},
	)
	upstream.totalLatencyMs.Add(latencyMs)
	if toolErr != nil {
		return s.handleToolErr(
			toolErrParams{
				Server: p.Execute.Server, Tool: p.Execute.Tool, LatencyMs: latencyMs,
				Err: toolErr, Session: p.Session,
			},
		)
	}
	return s.buildUpstreamEnvelope(
		envelopeParams{
			Entry: p.Entry, Tool: p.Execute.Tool, Raw: raw, Session: p.Session,
			Upstream: upstream, LatencyMs: latencyMs,
		},
	)
}

func (s *Server) buildUpstreamEnvelope(p envelopeParams) (any, error) {
	value, err := invoke.ExtractContent(p.Raw)
	if err != nil {
		return nil, err
	}
	p.Raw = value
	return s.buildEnvelope(p)
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
	s.logToolError(toolErrorLogParams{Server: p.Server, Tool: p.Tool, LatencyMs: p.LatencyMs, Err: p.Err})
	env := response.BuildError("tool_error", p.Err.Error(), false, "")
	projCfg := s.resolveProjection(p.Server, p.Tool, p.Session)
	return s.formatEnvelope(formatEnvelopeParams{Server: p.Server, Tool: p.Tool, Envelope: env, Projection: projCfg})
}

type envelopeParams struct {
	Entry     *registry.ToolEntry
	Tool      string
	Raw       json.RawMessage
	Session   *Session
	Upstream  *upstreamServer
	LatencyMs int64
}

func (s *Server) buildEnvelope(p envelopeParams) (any, error) {
	projCfg := s.resolveProjection(p.Entry.Server, p.Tool, p.Session)
	projStart := s.clock.Now()
	env, stats, err := s.buildProjectedEnvelope(
		projectedEnvelopeParams{Server: p.Entry.Server, Tool: p.Tool, Raw: p.Raw, ProjCfg: projCfg},
	)
	if err != nil {
		return nil, err
	}
	saved := int64(stats.RawTokens - stats.SummaryTokens)
	p.Upstream.recordSaved(p.Session, p.LatencyMs, saved)
	s.logProjection(p, stats, s.clock.Since(projStart).Milliseconds())
	return s.formatEnvelope(formatEnvelopeParams{
		Server: p.Entry.Server, Tool: p.Entry.ToolName.Name(), Envelope: env, Projection: projCfg,
	})
}

type projectedEnvelopeParams struct {
	Server  string
	Tool    string
	Raw     json.RawMessage
	ProjCfg *config.ProjectionConfig
}

func (s *Server) buildProjectedEnvelope(p projectedEnvelopeParams) (*response.Envelope, response.CallStats, error) {
	return invoke.BuildEnvelope(invoke.BuildEnvelopeParams{
		Server:   p.Server,
		Tool:     p.Tool,
		Raw:      p.Raw,
		ProjCfg:  p.ProjCfg,
		ProjDefs: s.projDefaults,
		Builder:  s.envelope,
	})
}

type formattedEnvelope struct {
	text    string
	isError bool
}

type formatEnvelopeParams struct {
	Server     string
	Tool       string
	Envelope   *response.Envelope
	Projection *config.ProjectionConfig
}

func (s *Server) formatEnvelope(p formatEnvelopeParams) (any, error) {
	projFormat := config.ProjectionFormat(p.Projection)
	if config.EffectiveFormat("", projFormat, s.cfg.ResponseFormat) == config.FormatToon {
		text, err := EncodeToon(s.logger.With("server", p.Server, "tool", p.Tool), p.Envelope)
		if err != nil {
			return nil, fmt.Errorf("encode TOON response: %w", err)
		}
		return formattedEnvelope{text: text, isError: p.Envelope.Error != ""}, nil
	}
	return p.Envelope, nil
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

func (s *Server) logProjection(p envelopeParams, stats response.CallStats, projectionMs int64) {
	saved := int64(stats.RawTokens - stats.SummaryTokens)
	s.logger.Debug(
		"projection applied",
		"server",
		p.Entry.Server,
		"tool",
		p.Tool,
		"upstream_ms",
		p.LatencyMs,
		"proj_ms",
		projectionMs,
		"raw_tokens",
		stats.RawTokens,
		"tokens_saved",
		saved,
	)
}
