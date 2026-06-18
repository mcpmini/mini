package server

import (
	"context"
	"time"

	"github.com/mcpmini/mini/internal/transport"
)

const toolsRefreshTimeout = 15 * time.Second

func (s *Server) onUpstreamNotification(serverName, method string) {
	if method != transport.NotificationToolsChanged {
		return
	}
	go s.refreshUpstreamTools(serverName) // on the upstream read loop; must not block it
}

func (s *Server) refreshUpstreamTools(serverName string) {
	u := s.upstreamByName(serverName)
	if u == nil || !u.beginRefresh() {
		return
	}
	for {
		s.relistAndNotify(u)
		if !u.endRefresh() {
			return
		}
	}
}

func (s *Server) relistAndNotify(u *upstreamServer) {
	ctx, cancel := context.WithTimeout(u.ctx, toolsRefreshTimeout)
	defer cancel()
	tools, err := u.currentConn().ListTools(ctx)
	if err != nil {
		s.logger.Warn("upstream tools refresh failed", "server", u.cfg.Name, "err", err)
		return
	}
	s.replaceRegistryToolsLocked(u, tools)
	s.notifyAllSessions()
	s.logger.Info("upstream tools refreshed", "server", u.cfg.Name, "tools", len(tools))
}

func (s *Server) upstreamByName(serverName string) *upstreamServer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.upstreams[serverName]
}
