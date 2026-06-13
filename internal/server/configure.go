package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

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
		s.notifyToolsChanged(session, p.Action)
	}
	return result, err
}

func (s *Server) notifyToolsChanged(session *Session, action string) {
	if action != "add_server" && action != "remove_server" {
		return
	}
	if session.toolMode() == ToolModePassthrough {
		s.notifyAllSessions()
		return
	}
	session.notify(toolsChangedNotif)
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
		return s.addServerRuntime(ctx, p)
	case "remove_server":
		return s.removeServerRuntime(p.ServerName)
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
	if p.SessionOnly {
		return s.setSessionProjection(session, p), nil
	}
	return s.setServerProjection(p)
}

func validateProjectionTarget(p configureParams) error {
	if p.Tool == "" {
		return fmt.Errorf("tool is required for set_projection")
	}
	if err := validateServerName(p.ServerName); err != nil {
		return err
	}
	if !config.ValidToolName.MatchString(p.Tool) {
		return fmt.Errorf("invalid tool name: %q", p.Tool)
	}
	return nil
}

func (s *Server) setSessionProjection(session *Session, p configureParams) any {
	fullName := toolFullName(p.ServerName, p.Tool)
	session.SetProjection(fullName, p.Projection)
	return map[string]any{"ok": true, "scope": "session", "tool": fullName}
}

func (s *Server) setServerProjection(p configureParams) (any, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	prev := s.storeServerProjection(p.ServerName, p.Tool, p.Projection)
	if err := s.persistProjectionsLocked(p.ServerName); err != nil {
		s.restoreServerProjection(p.ServerName, p.Tool, prev)
		return nil, fmt.Errorf("set_projection: persistence failed: %w", err)
	}
	return map[string]any{"ok": true, "scope": "server", "tool": toolFullName(p.ServerName, p.Tool)}, nil
}

func (s *Server) storeServerProjection(serverName, tool string, projection *config.ProjectionConfig) *config.ProjectionConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projections[serverName] == nil {
		s.projections[serverName] = make(map[string]*config.ProjectionConfig)
	}
	prev := s.projections[serverName][tool]
	s.projections[serverName][tool] = projection
	return prev
}

func (s *Server) restoreServerProjection(serverName, tool string, prev *config.ProjectionConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev != nil {
		s.projections[serverName][tool] = prev
		return
	}
	delete(s.projections[serverName], tool)
}

func (s *Server) reloadProjections() (any, error) {
	// Hold persistMu for the entire load+replace so we don't interleave with a
	// concurrent set_projection that has already updated the in-memory map but
	// hasn't yet flushed to disk: without this lock, reload could wipe the
	// in-memory update and then set_projection would persist the wiped state.
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	projections, err := loadServerProjections(s.configDir)
	if err != nil {
		return nil, fmt.Errorf("reload projections: %w", err)
	}
	s.replaceProjections(projections)
	return map[string]any{"ok": true, "loaded": projectionCounts(projections)}, nil
}

func (s *Server) replaceProjections(projections map[string]map[string]*config.ProjectionConfig) {
	s.mu.Lock()
	s.projections = projections
	s.mu.Unlock()
}

func projectionCounts(projections map[string]map[string]*config.ProjectionConfig) map[string]int {
	counts := make(map[string]int, len(projections))
	for serverName, tools := range projections {
		counts[serverName] = len(tools)
	}
	return counts
}

func (s *Server) addServerRuntime(ctx context.Context, p configureParams) (any, error) {
	if err := s.validateAddServerParams(p); err != nil {
		return nil, err
	}
	p.ServerCfg.RuntimeAdded = true
	if err := s.AddUpstream(ctx, *p.ServerCfg); err != nil {
		return nil, err
	}
	s.logger.Info("server added at runtime", "server", p.ServerCfg.Name)
	return map[string]any{"ok": true, "server": p.ServerCfg.Name}, nil
}

func (s *Server) validateAddServerParams(p configureParams) error {
	if p.ServerCfg == nil {
		return fmt.Errorf("config is required for add_server")
	}
	if err := validateServerName(p.ServerCfg.Name); err != nil {
		return err
	}
	return s.validateRuntimeTransport(p.ServerCfg)
}

func (s *Server) validateRuntimeTransport(sc *config.ServerConfig) error {
	switch sc.Transport {
	case "http", "sse", "streamable":
		return s.validateRuntimeHTTPTransport(sc)
	default:
		return s.validateRuntimeStdioTransport(sc)
	}
}

func (s *Server) validateRuntimeHTTPTransport(sc *config.ServerConfig) error {
	sc.Auth = nil
	sc.Headers = nil
	if s.cfg.DangerousAllowPrivateURLs {
		return nil
	}
	if err := transport.ValidateURL(sc.URL); err != nil {
		return fmt.Errorf("add_server: %w", err)
	}
	return nil
}

func (s *Server) validateRuntimeStdioTransport(sc *config.ServerConfig) error {
	if !s.cfg.DangerousAllowRuntimeStdio {
		return fmt.Errorf("add_server only supports http/sse/streamable transports at runtime; set dangerous_allow_runtime_stdio: true to enable stdio")
	}
	sc.Env = nil
	return nil
}

func (s *Server) removeServerRuntime(serverName string) (any, error) {
	if serverName == "" {
		return nil, fmt.Errorf("server is required for remove_server")
	}
	if err := validateServerName(serverName); err != nil {
		return nil, err
	}
	s.detachAndCloseServer(serverName)
	s.logger.Info("server removed at runtime", "server", serverName)
	return map[string]any{"ok": true, "server": serverName}, nil
}

func (s *Server) detachAndCloseServer(serverName string) {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	s.removeGen[serverName]++
	if u := s.detachUpstream(serverName); u != nil {
		u.shutdownAndClose()
	}
	s.sessions.closeServerConnections(serverName)
	s.reg.RemoveServer(serverName)
}

func (s *Server) detachUpstream(serverName string) *upstreamServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.upstreams[serverName]
	delete(s.upstreams, serverName)
	delete(s.projections, serverName)
	return u
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
	s.mu.RLock()
	defer s.mu.RUnlock()
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

func (s *Server) persistProjectionsLocked(serverName string) error {
	if err := validateServerName(serverName); err != nil {
		return err
	}
	b, err := s.marshalServerProjections(serverName)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.configDir, "projections")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, serverName+".yaml"), b, 0600)
}

func (s *Server) marshalServerProjections(serverName string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return yaml.Marshal(s.projections[serverName])
}
