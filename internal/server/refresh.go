package server

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

const refreshTimeout = 15 * time.Second

func (s *Server) attachNotificationHandler(u *upstreamServer, conn transport.Connection, gen uint64) {
	source, ok := conn.(transport.NotificationSource)
	if !ok {
		return
	}
	source.SetNotificationHandler(func(notification transport.Notification) {
		if notification.Method == transport.NotificationToolsChanged {
			s.requestToolRefresh(u, gen)
		}
	})
}

func (s *Server) requestToolRefresh(u *upstreamServer, gen uint64) {
	if !s.isCurrentConnectionGen(u, gen) {
		return
	}
	if !u.beginRefreshRequest() {
		return
	}
	if !s.startRefreshWorker() {
		u.finishRefreshRequest()
		return
	}
	go s.runToolRefreshes(u)
}

func (s *Server) runToolRefreshes(u *upstreamServer) {
	defer s.refreshWg.Done()
	for s.takeRefreshRequest(u) {
		if err := s.refreshTools(u); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Warn("refresh upstream tools failed", "server", u.cfg.Name, "err", err)
		}
	}
}

func (s *Server) takeRefreshRequest(u *upstreamServer) bool {
	u.refreshMu.Lock()
	defer u.refreshMu.Unlock()
	if !u.refreshAgain || u.ctx.Err() != nil {
		u.refreshing = false
		return false
	}
	u.refreshAgain = false
	return true
}

func (u *upstreamServer) beginRefreshRequest() bool {
	u.refreshMu.Lock()
	defer u.refreshMu.Unlock()
	u.refreshAgain = true
	if u.refreshing {
		return false
	}
	u.refreshing = true
	return true
}

func (u *upstreamServer) finishRefreshRequest() {
	u.refreshMu.Lock()
	u.refreshing = false
	u.refreshMu.Unlock()
}

func (s *Server) startRefreshWorker() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closing {
		return false
	}
	s.refreshWg.Add(1)
	return true
}

func (s *Server) refreshTools(u *upstreamServer) error {
	for attempt := range 3 {
		gen, tools, err := s.fetchCurrentTools(u)
		if err == nil {
			changed, published := s.publishRefreshedTools(u, gen, tools)
			if published && changed {
				s.notifyAllSessions()
			}
			return nil
		}
		if transport.IsConnectionError(err) {
			s.maybeReconnect(u, connError{err: err})
			return err
		}
		if attempt == 2 || !s.waitRefreshRetry(u, time.Second<<attempt) {
			return err
		}
	}
	return nil
}

func (s *Server) fetchCurrentTools(u *upstreamServer) (uint64, []transport.ToolDefinition, error) {
	state := u.connState()
	if state == nil {
		return 0, nil, context.Canceled
	}
	ctx, cancel := context.WithTimeout(u.ctx, refreshTimeout)
	defer cancel()
	tools, err := state.conn.ListTools(ctx)
	return state.gen, tools, err
}

func (s *Server) waitRefreshRetry(u *upstreamServer, delay time.Duration) bool {
	timer := s.clock.NewTimer(delay)
	select {
	case <-u.ctx.Done():
		timer.Stop()
		return false
	case <-timer.Chan():
		return true
	}
}

func (s *Server) publishRefreshedTools(u *upstreamServer, gen uint64, tools []transport.ToolDefinition) (bool, bool) {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()
	if !s.isCurrentConnectionGen(u, gen) {
		return false, false
	}
	before := buildProxyToolSchemas(s.reg.AllFull())
	u.lastDefs = tools
	s.reg.ReplaceServerTools(registry.ServerParams{
		Name:            u.cfg.Name,
		Defs:            tools,
		Perm:            u.cfg.Permissions,
		AliasByToolName: s.currentAliasesFor(u.cfg.Name),
	})
	after := buildProxyToolSchemas(s.reg.AllFull())
	return !reflect.DeepEqual(before, after), true
}

func (s *Server) isCurrentConnectionGen(u *upstreamServer, gen uint64) bool {
	if u.ctx.Err() != nil {
		return false
	}
	s.mu.RLock()
	current := s.upstreams[u.cfg.Name] == u
	s.mu.RUnlock()
	if !current {
		return false
	}
	return u.isCurrentConnGen(gen)
}
