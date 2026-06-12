package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/response"
)

func (s *Server) routeProxyTool(ctx context.Context, name string, args json.RawMessage, session *Session) (any, error) {
	switch name {
	case "config":
		return s.handleConfigure(ctx, args, session)
	case "read":
		return s.handleRead(ctx, args)
	default:
		return s.handleProxyCall(ctx, name, args, session)
	}
}

func (s *Server) handleProxyCall(ctx context.Context, name string, args json.RawMessage, session *Session) (any, error) {
	server, tool, err := parseProxyToolName(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidParams, err)
	}
	entry, err := s.reg.Lookup(server + "." + tool)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidParams, err)
	}
	params, err := unmarshalToolArgs(args)
	if err != nil {
		return nil, err
	}
	return s.proxyCallUpstream(ctx, proxyCallParams{Server: server, Tool: tool, Params: params, Entry: entry, Session: session})
}

func unmarshalToolArgs(args json.RawMessage) (map[string]any, error) {
	if len(args) == 0 || string(args) == "null" {
		return nil, nil
	}
	var params map[string]any
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("%w: unmarshal args: %w", errInvalidParams, err)
	}
	return params, nil
}

type proxyCallParams struct {
	Server  string
	Tool    string
	Params  map[string]any
	Entry   *registry.ToolEntry
	Session *Session
}

func (s *Server) proxyCallUpstream(ctx context.Context, p proxyCallParams) (any, error) {
	server, tool, params := resolveTarget(executeParams{Server: p.Server, Tool: p.Tool, Params: p.Params}, p.Entry)
	upstream, err := s.getUpstream(server)
	if err != nil {
		return nil, err
	}
	raw, latencyMs, toolErr := s.dispatchRaw(ctx, upstream, tool, params, p.Session)
	upstream.totalLatencyMs.Add(latencyMs)
	if toolErr != nil {
		p.Session.recordCall(latencyMs, 0, true)
		return response.BuildError("tool_error", toolErr.Error(), false, ""), nil
	}
	return s.proxyProject(envelopeParams{Server: server, Tool: tool, Raw: raw, Session: p.Session, Upstream: upstream, LatencyMs: latencyMs})
}

func (s *Server) proxyProject(p envelopeParams) (any, error) {
	projCfg := s.resolveProjection(p.Server, p.Tool, p.Session)
	if projCfg == nil {
		p.Session.recordCall(p.LatencyMs, 0, false)
		return string(p.Raw), nil
	}
	env, stats, err := s.buildProjectedEnvelope(p.Server, p.Tool, p.Raw, projCfg)
	if err != nil {
		return nil, err
	}
	p.Upstream.recordSaved(p.Session, p.LatencyMs, int64(stats.RawTokens-stats.SummaryTokens))
	isLarge := stats.SummaryTokens > s.cfg.InlineThreshold
	return s.renderProxyResult(p.Server, p.Tool, env, projCfg, isLarge), nil
}

func (s *Server) renderProxyResult(server, tool string, env *response.Envelope, projCfg *config.ProjectionConfig, isLarge bool) string {
	format := s.cfg.ResponseFormat
	if projCfg.Format != "" {
		format = projCfg.Format
	}
	if format == "mini" {
		return RenderLines(server, tool, env)
	}
	return formatProxyEnvelope(env, isLarge)
}

// formatProxyEnvelope formats a proxy response. Small responses with no
// projection note are inlined as-is. Any projection note (elided/omitted
// fields or a hint) is rendered with the projected data inline plus a
// pointer to the raw file. Large responses with no note fall back to a
// file pointer only, since Data may still be too large to inline.
func formatProxyEnvelope(env *response.Envelope, isLarge bool) string {
	switch {
	case !hasProjectionNote(env) && !isLarge:
		return marshalProxyData(env.Data)
	case hasProjectionNote(env):
		return formatProjectedInline(env)
	case env.File != nil:
		return "File: " + *env.File
	default:
		return marshalProxyData(env.Data)
	}
}

func hasProjectionNote(env *response.Envelope) bool {
	return len(env.Elided) > 0 || len(env.Omitted) > 0 || env.Hint != ""
}

func marshalProxyData(data any) string {
	b, _ := json.Marshal(data)
	return string(b)
}

func formatProjectedInline(env *response.Envelope) string {
	b, _ := json.MarshalIndent(env.Data, "", "  ")
	header := "[Projected — " + projectionNote(env) + "]"
	if env.File != nil {
		header += "\nFile: " + *env.File
	}
	return header + "\n" + string(b)
}

func omissionNote(o response.Omission) string {
	if o.Path == "" {
		return fmt.Sprintf("response truncated (%d chars)", o.Bytes)
	}
	return fmt.Sprintf("%s truncated (%d chars)", o.Path, o.Bytes)
}

func projectionNote(env *response.Envelope) string {
	var parts []string
	if len(env.Elided) > 0 {
		parts = append(parts, strings.Join(env.Elided, ", ")+" elided")
	}
	for _, o := range env.Omitted {
		parts = append(parts, omissionNote(o))
	}
	if env.Hint != "" {
		parts = append(parts, env.Hint)
	}
	return strings.Join(parts, "; ")
}

func parseProxyToolName(name string) (server, tool string, err error) {
	idx := strings.Index(name, "__")
	if idx < 0 {
		return "", "", fmt.Errorf("unknown proxy tool: %q (expected server__tool format)", name)
	}
	return name[:idx], name[idx+2:], nil
}
