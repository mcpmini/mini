package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

var toolsChangedNotif = json.RawMessage(`{"jsonrpc":"2.0","method":"` + transport.NotificationToolsChanged + `"}`)

type configureParams struct {
	Action      string                   `json:"action"`
	ServerName  string                   `json:"server"`
	Tool        string                   `json:"tool"`
	Projection  *config.ProjectionConfig `json:"projection"`
	ServerCfg   *config.ServerConfig     `json:"config"`
	SessionOnly bool                     `json:"session_only"`
}

func (s *Server) handleConfigure(ctx context.Context, raw json.RawMessage, session *Session) (any, error) {
	var p configureParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	result, err := s.dispatchConfigureAction(ctx, p, session)
	if err == nil {
		s.notifyToolsChanged(p.Action)
	}
	return result, err
}

func (s *Server) notifyToolsChanged(action string) {
	if action == "add_server" || action == "remove_server" {
		// Proxy and compact sessions share the daemon, so all need notification; a spurious
		// tools/list_changed to a compact session is harmless (it re-fetches the same 4 meta-tools).
		s.notifyAllSessions()
	}
}

func (s *Server) dispatchConfigureAction(ctx context.Context, p configureParams, session *Session) (any, error) {
	switch p.Action {
	case "status":
		return s.statusReport(), nil
	case "set_projection":
		return s.setProjection(session, p)
	case "reload":
		return s.reloadProjections()
	case "add_server":
		return s.addServerFromAgent(ctx, p.ServerCfg)
	case "remove_server":
		return s.removeServerFromAgent(p.ServerName)
	default:
		return s.dispatchConfigureAuthAction(p)
	}
}

func (s *Server) dispatchConfigureAuthAction(p configureParams) (any, error) {
	switch p.Action {
	case "start_auth":
		return s.handleStartAuth(p.ServerName)
	case "auth_status":
		return s.handleAuthStatus(p.ServerName)
	default:
		return nil, fmt.Errorf("unknown configure action: %s", p.Action)
	}
}

func (s *Server) setProjection(session *Session, p configureParams) (any, error) {
	if err := validateProjectionTarget(p); err != nil {
		return nil, err
	}
	visibleTool := p.Tool
	if entry, err := s.reg.Lookup(toolFullName(p.ServerName, p.Tool)); err == nil {
		p.Tool = entry.ToolName.UpstreamName
	}
	if p.SessionOnly {
		return s.setSessionProjection(session, p, visibleTool), nil
	}
	return s.setServerProjection(p, visibleTool)
}

func validateProjectionTarget(p configureParams) error {
	if p.Tool == "" {
		return fmt.Errorf("tool is required for set_projection")
	}
	if err := validateServerName(p.ServerName); err != nil {
		return err
	}
	if p.Tool != "*" && !config.ValidToolName.MatchString(p.Tool) {
		return fmt.Errorf("invalid tool name: %q", p.Tool)
	}
	if p.Projection != nil {
		if err := config.ValidResponseFormat(p.Projection.Format); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) setSessionProjection(session *Session, p configureParams, visibleTool string) any {
	fullName := toolFullName(p.ServerName, p.Tool)
	session.SetProjection(fullName, p.Projection)
	return map[string]any{"ok": true, "scope": "session", "tool": toolFullName(p.ServerName, visibleTool)}
}

func (s *Server) setServerProjection(p configureParams, visibleTool string) (any, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	projection, err := config.ReplaceServerProjection(config.ServerProjectionParams{
		ConfigDir: s.configDir, ServerName: p.ServerName, Tool: p.Tool, Projection: cloneProjectionRequest(p.Projection),
	})
	if err != nil {
		return nil, fmt.Errorf("set_projection: persistence failed: %w; fix the server file or pass session_only", err)
	}
	s.publishServerProjection(p.ServerName, p.Tool, projection)
	return map[string]any{"ok": true, "scope": "server", "tool": toolFullName(p.ServerName, visibleTool)}, nil
}

func cloneProjectionRequest(projection *config.ProjectionConfig) *config.ProjectionConfig {
	if projection == nil {
		return nil
	}
	copy := *projection
	return &copy
}

func (s *Server) publishServerProjection(serverName, tool string, projection *config.ProjectionConfig) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if projection == nil {
		delete(s.projections[serverName], tool)
		return
	}
	if s.projections[serverName] == nil {
		s.projections[serverName] = make(map[string]*config.ProjectionConfig)
	}
	s.projections[serverName][tool] = projection
}

func (s *Server) statusReport() map[string]any {
	servers, projInfo := s.collectStatusData()
	fileCount, usedBytes := s.store.Stats()
	return map[string]any{
		"servers":     servers,
		"store":       map[string]any{"files": fileCount, "used_mb": float64(usedBytes) / (1024 * 1024)},
		"projections": projInfo,
		"sessions":    s.sessions.aggregateMetrics(),
	}
}

func (s *Server) collectStatusData() (map[string]any, map[string][]string) {
	upstreamsCopy, projInfo := s.snapshotStatusInputs()
	return buildServerStatus(upstreamsCopy, s.reg), projInfo
}

func (s *Server) snapshotStatusInputs() (map[string]*upstreamServer, map[string][]string) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	upstreamsCopy := make(map[string]*upstreamServer, len(s.upstreams))
	for name, u := range s.upstreams {
		upstreamsCopy[name] = u
	}
	return upstreamsCopy, snapshotProjectionNames(s.projections)
}

func snapshotProjectionNames(projections map[string]map[string]*config.ProjectionConfig) map[string][]string {
	projInfo := make(map[string][]string)
	for srv, tools := range projections {
		names := make([]string, 0, len(tools))
		for t := range tools {
			names = append(names, t)
		}
		projInfo[srv] = names
	}
	return projInfo
}

func buildServerStatus(upstreams map[string]*upstreamServer, reg *registry.Registry) map[string]any {
	servers := make(map[string]any, len(upstreams))
	for name, u := range upstreams {
		info := u.stats()
		info["tools"] = reg.ToolCount(name)
		servers[name] = info
	}
	return servers
}
