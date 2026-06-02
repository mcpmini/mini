package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
		return s.handleRead(args)
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
	return s.proxyCallUpstream(ctx, server, tool, params, entry, session)
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

func (s *Server) proxyCallUpstream(ctx context.Context, server, tool string, params map[string]any, entry *registry.ToolEntry, session *Session) (any, error) {
	server, tool, params = resolveTarget(executeParams{Server: server, Tool: tool, Params: params}, entry)
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
	return s.proxyProject(server, tool, raw, session, upstream, latencyMs)
}

func (s *Server) proxyProject(server, tool string, raw json.RawMessage, session *Session, upstream *upstreamServer, latencyMs int64) (any, error) {
	projCfg := s.resolveProjection(server, tool, session)
	if projCfg == nil {
		session.recordCall(latencyMs, 0, false)
		return string(raw), nil
	}
	env, stats, err := s.buildProjectedEnvelope(server, tool, raw, projCfg)
	if err != nil {
		return nil, err
	}
	upstream.recordSaved(session, latencyMs, int64(stats.RawTokens-stats.SummaryTokens))
	return s.renderProxyResult(server, tool, env, projCfg, stats.SummaryTokens), nil
}

func (s *Server) renderProxyResult(server, tool string, env *response.Envelope, projCfg *config.ProjectionConfig, rawTokens int) string {
	format := s.cfg.ResponseFormat
	if projCfg.Format != "" {
		format = projCfg.Format
	}
	if format == "mini" {
		return RenderLines(server, tool, env)
	}
	return s.formatProxyEnvelope(env, rawTokens)
}

// formatProxyEnvelope formats a proxy response using the 3-tier approach:
// - No projection, small: raw JSON, mini invisible
// - Projection applied, small: bracket note + inline projected JSON
// - Large (above inline threshold): note (if projection) + file path
func (s *Server) formatProxyEnvelope(env *response.Envelope, rawTokens int) string {
	hasNote := len(env.Elided) > 0 || len(env.Truncated) > 0
	isLarge := rawTokens > s.cfg.InlineThreshold
	switch {
	case !hasNote && !isLarge:
		return marshalProxyData(env.Data)
	case !isLarge:
		return formatProjectedInline(env)
	case hasNote:
		return formatProjectedFile(env)
	case env.File != nil:
		return "File: " + *env.File
	default:
		return marshalProxyData(env.Data)
	}
}

func marshalProxyData(data any) string {
	b, _ := json.Marshal(data)
	return string(b)
}

func formatProjectedInline(env *response.Envelope) string {
	b, _ := json.MarshalIndent(env.Data, "", "  ")
	return "[Projected — " + projectionNote(env) + "]\n" + string(b)
}

func formatProjectedFile(env *response.Envelope) string {
	if env.File == nil {
		return formatProjectedInline(env)
	}
	return "[Projected — " + projectionNote(env) + "]\nFile: " + *env.File
}

func projectionNote(env *response.Envelope) string {
	var parts []string
	if len(env.Elided) > 0 {
		parts = append(parts, strings.Join(env.Elided, ", ")+" elided")
	}
	fields := sortedTruncatedFields(env.Truncated)
	for _, field := range fields {
		parts = append(parts, fmt.Sprintf("%s truncated (%d chars)", field, env.Truncated[field]))
	}
	return strings.Join(parts, "; ")
}

func sortedTruncatedFields(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// stable order for deterministic output
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (s *Server) handleRead(raw json.RawMessage) (any, error) {
	path, err := parseReadPath(raw)
	if err != nil {
		return nil, err
	}
	if err := s.validateStorePath(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	return string(b), nil
}

func parseReadPath(raw json.RawMessage) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("%w: read: %w", errInvalidParams, err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("%w: read: path is required", errInvalidParams)
	}
	return p.Path, nil
}

func (s *Server) validateStorePath(path string) error {
	// EvalSymlinks resolves symlinks on both sides so a symlink inside the store
	// dir pointing outside it cannot escape the confinement. On macOS, TempDir
	// returns /var/... which is itself a symlink to /private/var/..., so both
	// sides must be resolved for the prefix check to work correctly.
	storeDir := resolveSymlinks(s.store.Dir())
	abs := resolveSymlinks(path)
	if !strings.HasPrefix(abs, storeDir+string(filepath.Separator)) {
		return fmt.Errorf("%w: read: path must be within mini response directory", errInvalidParams)
	}
	return nil
}

// resolveSymlinks resolves symlinks, falling back to filepath.Abs if the path
// does not exist yet (file written but not yet visible, or non-existent path).
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
	return name[:idx], name[idx+2:], nil
}
