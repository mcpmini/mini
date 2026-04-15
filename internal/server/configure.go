package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

var notifyToolsChanged = json.RawMessage(`{"jsonrpc":"2.0","method":"` + transport.NotificationToolsChanged + `"}`)

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
	if err == nil && (p.Action == "add_server" || p.Action == "remove_server") {
		if s.proxyMode {
			s.notifyAllSessions()
		} else {
			session.notify(notifyToolsChanged)
		}
	}
	return result, err
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
	case "start_auth":
		return s.handleStartAuth(p.ServerName)
	case "auth_status":
		return s.handleAuthStatus(p.ServerName)
	default:
		return nil, fmt.Errorf("unknown configure action: %s", p.Action)
	}
}

func (s *Server) setProjection(session *Session, p configureParams) (any, error) {
	if p.Tool == "" {
		return nil, fmt.Errorf("tool is required for set_projection")
	}
	if !config.ValidServerName.MatchString(p.ServerName) {
		return nil, fmt.Errorf("invalid server name: %q", p.ServerName)
	}
	if !config.ValidToolName.MatchString(p.Tool) {
		return nil, fmt.Errorf("invalid tool name: %q", p.Tool)
	}
	if p.SessionOnly {
		return s.setSessionProjection(session, p), nil
	}
	return s.setServerProjection(p)
}

func (s *Server) setSessionProjection(session *Session, p configureParams) any {
	fullName := toolFullName(p.ServerName, p.Tool)
	session.SetProjection(fullName, p.Projection)
	return map[string]any{"ok": true, "scope": "session", "tool": fullName}
}

func (s *Server) setServerProjection(p configureParams) (any, error) {
	s.mu.Lock()
	if s.projections[p.ServerName] == nil {
		s.projections[p.ServerName] = make(map[string]*config.ProjectionConfig)
	}
	prev := s.projections[p.ServerName][p.Tool]
	s.projections[p.ServerName][p.Tool] = p.Projection
	s.mu.Unlock()

	if err := s.persistProjections(p.ServerName); err != nil {
		s.mu.Lock()
		if prev != nil {
			s.projections[p.ServerName][p.Tool] = prev
		} else {
			delete(s.projections[p.ServerName], p.Tool)
		}
		s.mu.Unlock()
		return nil, fmt.Errorf("set_projection: persistence failed: %w", err)
	}
	return map[string]any{"ok": true, "scope": "server", "tool": toolFullName(p.ServerName, p.Tool)}, nil
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
	s.mu.Lock()
	s.projections = projections
	s.mu.Unlock()
	counts := make(map[string]int, len(projections))
	for srv, tools := range projections {
		counts[srv] = len(tools)
	}
	return map[string]any{"ok": true, "loaded": counts}, nil
}

func (s *Server) addServerRuntime(ctx context.Context, p configureParams) (any, error) {
	if p.ServerCfg == nil {
		return nil, fmt.Errorf("config is required for add_server")
	}
	if !config.ValidServerName.MatchString(p.ServerCfg.Name) {
		return nil, fmt.Errorf("invalid server name: %q", p.ServerCfg.Name)
	}
	if err := s.validateRuntimeTransport(p.ServerCfg); err != nil {
		return nil, err
	}
	if err := s.AddUpstream(ctx, *p.ServerCfg); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "server": p.ServerCfg.Name}, nil
}

func (s *Server) validateRuntimeTransport(sc *config.ServerConfig) error {
	switch sc.Transport {
	case "http", "sse", "streamable":
		// Strip agent-supplied credentials to prevent exfiltration via SSRF.
		sc.Auth = nil
		sc.Headers = nil
		if s.cfg.DangerousAllowPrivateURLs {
			return nil
		}
		if err := transport.ValidateURL(sc.URL); err != nil {
			return fmt.Errorf("add_server: %w", err)
		}
		return nil
	default:
		if !s.cfg.DangerousAllowRuntimeStdio {
			return fmt.Errorf("add_server only supports http/sse/streamable transports at runtime; set dangerous_allow_runtime_stdio: true to enable stdio")
		}
		// Strip agent-supplied env to prevent injecting credentials into subprocesses.
		sc.Env = nil
		return nil
	}
}

func (s *Server) removeServerRuntime(serverName string) (any, error) {
	if serverName == "" {
		return nil, fmt.Errorf("server is required for remove_server")
	}
	if !config.ValidServerName.MatchString(serverName) {
		return nil, fmt.Errorf("invalid server name: %q", serverName)
	}
	s.mu.Lock()
	u, ok := s.upstreams[serverName]
	if ok {
		delete(s.upstreams, serverName)
	}
	s.mu.Unlock()
	if ok {
		u.shutdown()
		u.mu.Lock()
		u.conn.Close()
		u.mu.Unlock()
	}

	s.sessions.closeServerConnections(serverName)
	s.reg.RemoveServer(serverName)
	return map[string]any{"ok": true, "server": serverName}, nil
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
	s.mu.RLock()
	upstreamsCopy := make(map[string]*upstreamServer, len(s.upstreams))
	for name, u := range s.upstreams {
		upstreamsCopy[name] = u
	}
	projInfo := make(map[string][]string)
	for srv, tools := range s.projections {
		names := make([]string, 0, len(tools))
		for t := range tools {
			names = append(names, t)
		}
		projInfo[srv] = names
	}
	s.mu.RUnlock()

	servers := make(map[string]any, len(upstreamsCopy))
	for name, u := range upstreamsCopy {
		info := u.stats()
		info["tools"] = s.reg.ToolCount(name)
		servers[name] = info
	}
	return servers, projInfo
}

func (s *Server) persistProjections(serverName string) error {
	if !config.ValidServerName.MatchString(serverName) {
		return fmt.Errorf("invalid server name: %q", serverName)
	}

	s.mu.RLock()
	b, err := yaml.Marshal(s.projections[serverName])
	s.mu.RUnlock()
	if err != nil {
		return err
	}

	dir := filepath.Join(s.configDir, "projections")
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, serverName+".yaml"), b, 0600)
}
