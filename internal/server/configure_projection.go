package server

import (
	"fmt"

	"github.com/mcpmini/mini/internal/config"
)

type projectionRules struct {
	Session *config.ProjectionConfig `json:"session,omitempty"`
	Server  *config.ProjectionConfig `json:"server,omitempty"`
}

func (s *Server) projectionRules(server, tool string, session *Session) projectionRules {
	rules := projectionRules{Session: session.Projection(toolFullName(server, tool))}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	rules.Server = s.projections[server][tool]
	return rules
}

func (s *Server) getProjection(session *Session, p configureParams) (any, error) {
	if err := validateProjectionTarget(p); err != nil {
		return nil, err
	}
	if !s.isKnownServer(p.ServerName) {
		return nil, fmt.Errorf("unknown server %q", p.ServerName)
	}
	tool, listed := s.upstreamToolName(p.ServerName, p.Tool)
	rules := s.projectionRules(p.ServerName, tool, session)
	if !listed && rules.Session == nil && rules.Server == nil && s.isUpstreamRegistered(p.ServerName) {
		return nil, fmt.Errorf("server %q has no tool %q", p.ServerName, p.Tool)
	}
	return map[string]any{"tool": toolFullName(p.ServerName, p.Tool), "rules": rules}, nil
}

func (s *Server) upstreamToolName(server, visibleTool string) (tool string, listed bool) {
	if tool, ok := s.toolForAlias(server, visibleTool); ok {
		return tool, true
	}
	if entry, ok := s.reg.LookupWithHidden(toolFullName(server, visibleTool)); ok {
		return entry.ToolName.UpstreamName, true
	}
	return visibleTool, false
}

func (s *Server) toolForAlias(server, alias string) (string, bool) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	for tool, rule := range s.projections[server] {
		if rule != nil && rule.Alias == alias {
			return tool, true
		}
	}
	return "", false
}

func (s *Server) setProjection(session *Session, p configureParams) (any, error) {
	if err := validateProjectionTarget(p); err != nil {
		return nil, err
	}
	if p.Projection != nil {
		if err := config.ValidResponseFormat(p.Projection.Format); err != nil {
			return nil, err
		}
	}
	visibleTool := p.Tool
	p.Tool, _ = s.upstreamToolName(p.ServerName, p.Tool)
	if p.SessionOnly {
		return s.setSessionProjection(session, p, visibleTool), nil
	}
	return s.setServerProjection(p, visibleTool)
}

func validateProjectionTarget(p configureParams) error {
	if p.Tool == "" {
		return fmt.Errorf("tool is required for %s", p.Action)
	}
	if err := validateServerName(p.ServerName); err != nil {
		return err
	}
	if !config.ValidToolName.MatchString(p.Tool) {
		return fmt.Errorf("invalid tool name: %q", p.Tool)
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

	projection, err := config.SaveServerProjection(config.ServerProjectionParams{
		ConfigDir: s.configDir, ServerName: p.ServerName, Tool: p.Tool, Projection: p.Projection,
	})
	if err != nil {
		return nil, fmt.Errorf("set_projection: not saved: %w; pass session_only:true to apply it to this session only", err)
	}
	s.publishServerProjection(p.ServerName, p.Tool, projection)
	return map[string]any{"ok": true, "scope": "server", "tool": toolFullName(p.ServerName, visibleTool)}, nil
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
