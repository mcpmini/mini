package server

import (
	"cmp"
	"context"
	"slices"

	"github.com/mcpmini/mini/internal/config"
)

type reconcilePlan struct {
	remove  []string
	connect []config.ServerConfig
}

func planReconcile(prev map[string]config.ServerConfig, set config.ServerSet) (reconcilePlan, map[string]config.ServerConfig) {
	var plan reconcilePlan
	next := make(map[string]config.ServerConfig)
	for name, sc := range set.Servers {
		if sc.IsEnabled() {
			next[name] = sc
		}
	}
	for _, old := range prev {
		planExisting(&plan, next, set, old)
	}
	for name, sc := range next {
		if _, known := prev[name]; !known {
			plan.connect = append(plan.connect, sc)
		}
	}
	slices.Sort(plan.remove)
	slices.SortFunc(plan.connect, func(a, b config.ServerConfig) int { return cmp.Compare(a.Name, b.Name) })
	return plan, next
}

func planExisting(plan *reconcilePlan, next map[string]config.ServerConfig, set config.ServerSet, old config.ServerConfig) {
	name := old.Name
	_, skipped := set.Skipped[name]
	_, loaded := set.Servers[name]
	current, wanted := next[name]
	switch {
	case skipped, !loaded && len(set.SourceErrors) > 0:
		next[name] = old
	case !wanted:
		plan.remove = append(plan.remove, name)
	case !config.SameServerSettings(old, current):
		plan.remove = append(plan.remove, name)
		plan.connect = append(plan.connect, current)
	}
}

func (s *Server) loadReconcileBaseline() map[string]config.ServerConfig {
	_, baseline := planReconcile(nil, config.LoadServerSet(s.configDir))
	return baseline
}

func (s *Server) planFromDisk(prev map[string]config.ServerConfig) (reconcilePlan, map[string]config.ServerConfig) {
	set := config.LoadServerSet(s.configDir)
	s.logServerSetProblems(set)
	return planReconcile(prev, set)
}

func (s *Server) logServerSetProblems(set config.ServerSet) {
	for _, se := range set.SourceErrors {
		s.logger.Warn("server reconcile: source error, no servers removed this pass", "path", se.Path, "err", se.Err)
	}
	for name, err := range set.Skipped {
		s.logger.Warn("server reconcile: keeping previous state for invalid server", "server", name, "err", err)
	}
}

func (s *Server) removeReconciled(names []string) {
	removed := false
	for _, name := range names {
		if s.isRuntimeAdded(name) {
			s.logger.Warn("server reconcile: leaving runtime-added server", "server", name)
			continue
		}
		s.detachAndCloseServer(name)
		s.logger.Info("server removed by config change", "server", name)
		removed = true
	}
	if removed {
		s.notifyAllSessions()
	}
}

func (s *Server) isRuntimeAdded(name string) bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	u := s.upstreams[name]
	return u != nil && u.cfg.RuntimeAdded
}

func (s *Server) connectReconciled(ctx context.Context, servers []config.ServerConfig) {
	for _, sc := range servers {
		if s.isRuntimeAdded(sc.Name) {
			s.logger.Warn("server reconcile: a runtime-added server has this name, not connecting the configured one", "server", sc.Name)
			continue
		}
		s.logger.Info("connecting server from config change", "server", sc.Name)
		s.startReconciledConnect(ctx, config.WithKnownAuth(s.configDir, sc))
	}
}

// Add must not race Close's connectWg.Wait, and Close must be able to cancel
// these connects even when the caller's ctx outlives the server.
func (s *Server) startReconciledConnect(ctx context.Context, sc config.ServerConfig) {
	s.connectMu.Lock()
	defer s.connectMu.Unlock()
	if s.connectsClosed {
		return
	}
	if s.reconcileConnectCtx == nil {
		s.reconcileConnectCtx, s.cancelReconcileConnect = context.WithCancel(ctx)
	}
	s.connectWg.Add(1)
	go s.connectUpstreamAsync(s.reconcileConnectCtx, s.startupInstall(sc))
}

func (s *Server) stopReconciledConnects() {
	s.connectMu.Lock()
	defer s.connectMu.Unlock()
	s.connectsClosed = true
	if s.cancelReconcileConnect != nil {
		s.cancelReconcileConnect()
	}
}
