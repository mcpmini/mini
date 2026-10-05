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
	case "get_projection":
		return s.getProjection(session, p)
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
