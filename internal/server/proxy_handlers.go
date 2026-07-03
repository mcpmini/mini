package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	case "execute_code":
		return s.handleExecuteCode(ctx, args)
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
	req, err := parseProxyRequest(args)
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
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return proxyRequest{}, fmt.Errorf("%w: unmarshal call arguments: %w", errInvalidParams, err)
	}
	if err := rejectLegacyFields(envelope); err != nil {
		return proxyRequest{}, err
	}
	args, err := extractProxyArgs(envelope)
	if err != nil {
		return proxyRequest{}, err
	}
	controls, err := extractProxyControls(envelope)
	if err != nil {
		return proxyRequest{}, err
	}
	return proxyRequest{Args: args, Controls: controls}, nil
}

func rejectLegacyFields(envelope map[string]json.RawMessage) error {
	var extra []string
	for key := range envelope {
		if key != "args" && key != "__mini" {
			extra = append(extra, key)
		}
	}
	if len(extra) == 0 {
		return nil
	}
	sort.Strings(extra)
	return fmt.Errorf("%w: legacy flat tool call — upstream arguments must be nested under \"args\" (unexpected field(s): %s), e.g. {\"args\": {...}}",
		errInvalidParams, strings.Join(extra, ", "))
}

func extractProxyArgs(envelope map[string]json.RawMessage) (map[string]any, error) {
	raw, ok := envelope["args"]
	if !ok || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var args map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("%w: args must be an object: %w", errInvalidParams, err)
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

func extractProxyControls(envelope map[string]json.RawMessage) (proxyControls, error) {
	raw, ok := envelope["__mini"]
	if !ok || string(raw) == "null" {
		return proxyControls{}, nil
	}
	var c struct {
		Projection projectionMode `json:"projection"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return proxyControls{}, fmt.Errorf("%w: __mini must be an object: %w", errInvalidParams, err)
	}
	if !c.Projection.Valid() {
		return proxyControls{}, fmt.Errorf("%w: __mini.projection must be \"default\" or \"raw\", got %q", errInvalidParams, c.Projection)
	}
	return proxyControls{Projection: c.Projection}, nil
}

type proxyCallParams struct {
	Server   string
	Tool     string
	Params   map[string]any
	Entry    *registry.ToolEntry
	Session  *Session
	Controls proxyControls
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
	ep := envelopeParams{Entry: p.Entry, Tool: tool, Raw: raw, Session: p.Session, Upstream: upstream, LatencyMs: latencyMs}
	ep.Bypass = p.Controls.Projection == projectionRaw
	return s.proxyProject(ep)
}

func (s *Server) proxyProject(p envelopeParams) (any, error) {
	var projCfg *config.ProjectionConfig
	if !p.Bypass {
		projCfg = s.resolveProjection(p.Entry.Server, p.Tool, p.Session)
	}
	env, stats, err := s.buildProjectedEnvelope(projectedEnvelopeParams{
		Server:  p.Entry.Server,
		Tool:    p.Tool,
		Raw:     p.Raw,
		ProjCfg: projCfg,
		Bypass:  p.Bypass,
	})
	if err != nil {
		return nil, err
	}
	p.Upstream.recordSaved(p.Session, p.LatencyMs, int64(stats.RawTokens-stats.SummaryTokens))
	return response.NewProxyResult(env), nil
}

func (s *Server) handleRead(ctx context.Context, raw json.RawMessage) (any, error) {
	file, filter, err := parseReadArgs(raw)
	if err != nil {
		return nil, err
	}
	file = s.resolveReadPath(file)
	if err := s.validateStorePath(file); err != nil {
		return nil, err
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
	// EvalSymlinks on both sides prevents a symlink inside the store from escaping
	// confinement; on macOS, TempDir itself is a symlink (/var/... → /private/var/...).
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
