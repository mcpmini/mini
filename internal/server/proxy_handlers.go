package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/response"
)

func (s *Server) routeProxyTool(ctx context.Context, name string, args json.RawMessage, session *Session) (any, error) {
	switch name {
	case "mini_config":
		return s.handleConfigure(ctx, args, session)
	case "mini_read":
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
		return nil, fmt.Errorf("%w: %s", errInvalidParams, err)
	}
	var params map[string]any
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &params); err != nil {
			return nil, fmt.Errorf("%w: unmarshal args: %w", errInvalidParams, err)
		}
	}
	return s.proxyCallUpstream(ctx, server, tool, params, entry, session)
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

	projCfg := s.resolveProjection(server, tool, session)
	if projCfg == nil {
		session.recordCall(latencyMs, 0, false)
		return string(raw), nil
	}

	env, stats, err := s.buildProjectedEnvelope(server, tool, raw, projCfg)
	if err != nil {
		return nil, err
	}
	saved := int64(stats.RawTokens - stats.SummaryTokens)
	if saved > 0 {
		upstream.estTokensSaved.Add(saved)
	}
	session.recordCall(latencyMs, saved, false)
	return s.formatProxyEnvelope(env, stats.RawTokens), nil
}

// formatProxyEnvelope formats a proxy response using the 3-tier approach:
// - No projection, small: raw JSON, mini invisible
// - Projection applied, small: bracket note + inline projected JSON
// - Large (above inline threshold): note (if projection) + file path
func (s *Server) formatProxyEnvelope(env *response.Envelope, rawTokens int) string {
	hasNote := len(env.Elided) > 0 || len(env.Truncated) > 0
	isLarge := rawTokens > s.cfg.InlineThreshold

	if !hasNote && !isLarge {
		b, _ := json.Marshal(env.Data)
		return string(b)
	}

	if !isLarge {
		b, _ := json.MarshalIndent(env.Data, "", "  ")
		return "[Projected — " + projectionNote(env) + "]\n" + string(b)
	}

	if hasNote {
		return "[Projected — " + projectionNote(env) + "]\nFile: " + *env.File
	}
	if env.File != nil {
		return "File: " + *env.File
	}
	b, _ := json.Marshal(env.Data)
	return string(b)
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
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%w: mini_read: %w", errInvalidParams, err)
	}
	if p.Path == "" {
		return nil, fmt.Errorf("%w: mini_read: path is required", errInvalidParams)
	}
	if err := s.validateStorePath(p.Path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p.Path)
	if err != nil {
		return nil, fmt.Errorf("mini_read: %w", err)
	}
	return string(b), nil
}

func (s *Server) validateStorePath(path string) error {
	storeDir := s.store.Dir()
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("%w: mini_read: invalid path: %w", errInvalidParams, err)
	}
	if !strings.HasPrefix(abs, storeDir+string(filepath.Separator)) {
		return fmt.Errorf("%w: mini_read: path must be within mini response directory", errInvalidParams)
	}
	return nil
}

func parseProxyToolName(name string) (server, tool string, err error) {
	idx := strings.Index(name, "__")
	if idx < 0 {
		return "", "", fmt.Errorf("unknown proxy tool: %q (expected server__tool format)", name)
	}
	return name[:idx], name[idx+2:], nil
}
