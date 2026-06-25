package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/jq"
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
	raw, latencyMs, toolErr := s.dispatchRaw(ctx, dispatchParams{Upstream: upstream, Tool: tool, Params: params, Session: p.Session})
	upstream.totalLatencyMs.Add(latencyMs)
	if toolErr != nil {
		p.Session.recordCall(latencyMs, 0, true)
		return response.BuildError("tool_error", toolErr.Error(), false, ""), nil
	}
	return s.proxyProject(envelopeParams{Entry: p.Entry, Tool: tool, Raw: raw, Session: p.Session, Upstream: upstream, LatencyMs: latencyMs})
}

func (s *Server) proxyProject(p envelopeParams) (any, error) {
	projCfg := s.resolveProjection(p.Entry.Server, p.Tool, p.Session)
	env, stats, err := s.buildProjectedEnvelope(p.Entry.Server, p.Tool, p.Raw, projCfg)
	if err != nil {
		return nil, err
	}
	p.Upstream.recordSaved(p.Session, p.LatencyMs, int64(stats.RawTokens-stats.SummaryTokens))
	return s.renderProxyResult(renderProxyResultParams{Server: p.Entry.Server, Tool: p.Entry.ToolName.Name(), Env: env, ProjCfg: projCfg}), nil
}

type renderProxyResultParams struct {
	Server  string
	Tool    string
	Env     *response.Envelope
	ProjCfg *config.ProjectionConfig
}

func (s *Server) renderProxyResult(p renderProxyResultParams) string {
	format := s.cfg.ResponseFormat
	if p.ProjCfg != nil && p.ProjCfg.Format != "" {
		format = p.ProjCfg.Format
	}
	if format == "mini" {
		return RenderLines(p.Server, p.Tool, p.Env)
	}
	return formatProxyEnvelope(p.Env)
}

func formatProxyEnvelope(env *response.Envelope) string {
	if !hasProjectionNote(env) {
		return marshalProxyData(env.Data)
	}
	return formatProjectedInline(env)
}

func hasProjectionNote(env *response.Envelope) bool {
	return len(env.Excluded) > 0 || len(env.Truncated) > 0
}

func marshalProxyData(data any) string {
	b, _ := json.Marshal(data)
	return string(b)
}

func formatProjectedInline(env *response.Envelope) string {
	meta := map[string]any{}
	if len(env.Excluded) > 0 {
		meta["excluded"] = env.Excluded
	}
	if len(env.Truncated) > 0 {
		meta["truncated"] = env.Truncated
	}
	if len(env.Excluded) > 0 || len(env.Truncated) > 0 {
		meta["msg"] = "Response filtered, some fields were excluded or truncated. Use read(<file>, <jq filter>) to fetch full values."
	}
	if len(env.Passthrough) > 0 {
		meta["passthrough"] = env.Passthrough
	}
	if env.File != nil {
		meta["file"] = *env.File
	}
	out := map[string]any{"__mini": meta, "data": env.Data}
	b, _ := json.Marshal(out)
	return string(b)
}

func (s *Server) handleRead(ctx context.Context, raw json.RawMessage) (any, error) {
	path, filter, err := parseReadArgs(raw)
	if err != nil {
		return nil, err
	}
	if err := s.validateStorePath(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
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

func parseReadArgs(raw json.RawMessage) (path, filter string, err error) {
	var p struct {
		Path   string `json:"path"`
		Filter string `json:"filter"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", "", fmt.Errorf("%w: read: %w", errInvalidParams, err)
	}
	if p.Path == "" {
		return "", "", fmt.Errorf("%w: read: path is required", errInvalidParams)
	}
	return p.Path, p.Filter, nil
}

func (s *Server) validateStorePath(path string) error {
	// On macOS, TempDir returns /var/... which is a symlink to /private/var/...,
	// so both sides must be resolved for the prefix check to work correctly.
	storeDir := resolveSymlinks(s.store.Dir())
	abs := resolveSymlinks(path)
	if !strings.HasPrefix(abs, storeDir+string(filepath.Separator)) {
		return fmt.Errorf("%w: read: path must be within mini response directory", errInvalidParams)
	}
	return nil
}

// Falls back to filepath.Abs for paths that do not exist yet (agent-provided paths
// that haven't been created, or files cleaned up between validation and read).
func resolveSymlinks(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	abs, _ := filepath.Abs(path)
	return abs
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
