package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/registry"
)

const configPollInterval = 5 * time.Second

// StartConfigReload applies server and projection edits on disk to a live
// server without restart. Stops when ctx is canceled.
func (s *Server) StartConfigReload(ctx context.Context) {
	go s.runConfigReload(ctx, nil)
}

func (s *Server) runConfigReload(ctx context.Context, afterCheck func()) {
	if afterCheck == nil {
		afterCheck = func() {}
	}
	last, _ := s.fingerprintOrWarn()
	// Startup read the config before this poller existed; an edit made in
	// between is only caught by applying the config once now.
	s.applyConfig()
	ticker := s.clock.NewTicker(configPollInterval)
	defer ticker.Stop()
	afterCheck()
	for {
		select {
		case <-ticker.Chan():
			last = s.reloadIfConfigChanged(last)
			afterCheck()
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) reloadIfConfigChanged(last map[string]string) map[string]string {
	current, ok := s.fingerprintOrWarn()
	if !ok {
		return last
	}
	changed := changedPaths(last, current)
	if len(changed) == 0 {
		return last
	}
	if fresh := s.applyConfig(); len(fresh) > 0 {
		s.logger.Info("projections reloaded", "files", changed)
	}
	return current
}

func (s *Server) applyConfig() map[string]int {
	servers, fresh, err := s.applyReload()
	if err != nil {
		s.logger.Warn("config reload: keeping the current config", "err", err)
		return nil
	}
	s.removeServersGoneFromConfig(servers)
	return fresh
}

func (s *Server) fingerprintOrWarn() (map[string]string, bool) {
	fp, err := fingerprintConfigSources(s.configDir)
	if err != nil {
		s.logger.Warn("config reload: fingerprint config sources", "err", err)
		return nil, false
	}
	return fp, true
}

func fingerprintConfigSources(configDir string) (map[string]string, error) {
	paths, err := config.ServerDirFiles(configDir)
	if err != nil {
		return nil, err
	}
	fp := make(map[string]string, len(paths))
	for _, p := range paths {
		if h, ok := fileFingerprint(p); ok {
			fp[p] = h
		}
	}
	return fp, nil
}

const unreadableFingerprint = "unreadable"

func fileFingerprint(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		// Not a scan failure: the loader reports it and holds back only this file's server.
		return unreadableFingerprint, true
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true
}

func changedPaths(prev, curr map[string]string) []string {
	var changed []string
	for p, h := range curr {
		if prev[p] != h {
			changed = append(changed, p)
		}
	}
	for p := range prev {
		if _, ok := curr[p]; !ok {
			changed = append(changed, p)
		}
	}
	sort.Strings(changed)
	return changed
}

func (s *Server) applyReload() (config.Servers, map[string]int, error) {
	// Hold persistMu for the entire load+replace so we don't interleave with a
	// concurrent set_projection that has already updated the in-memory map but
	// hasn't yet flushed to disk: without this lock, reload could wipe the
	// in-memory update and then set_projection would persist the wiped state.
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	servers, err := config.LoadServers(s.configDir)
	if err != nil {
		return config.Servers{}, nil, err
	}
	s.logReloadProblems(servers)
	projections := serverProjections(servers.Loaded)
	fresh := projectionCounts(projections)
	s.replaceProjections(projections, servers)
	s.reapplyAliases()
	return servers, fresh, nil
}

func (s *Server) reloadProjections() (any, error) {
	servers, fresh, err := s.applyReload()
	if err != nil {
		return nil, err
	}
	return buildReloadResult(servers, fresh), nil
}

func buildReloadResult(servers config.Servers, fresh map[string]int) map[string]any {
	result := map[string]any{
		"ok":     !servers.HasProblems(),
		"loaded": fresh,
	}
	if servers.HasProblems() {
		result["source_errors"] = sourceErrorPaths(servers.Problems())
	}
	return result
}

func sourceErrorPaths(errors []config.SourceError) []string {
	paths := make([]string, len(errors))
	for i, se := range errors {
		paths[i] = se.Path
	}
	slices.Sort(paths)
	return paths
}

func (s *Server) logReloadProblems(servers config.Servers) {
	for _, se := range servers.Broken {
		s.logger.Warn("server config fails to load, "+s.brokenServerOutcome(se.ServerName), "server", se.ServerName, "path", se.Path, "err", se.Err)
	}
	for _, se := range servers.BrokenProjections() {
		s.logger.Warn("projections fail to load, "+s.brokenProjectionsOutcome(se.ServerName), "server", se.ServerName, "path", se.Path, "err", se.Err)
	}
}

func (s *Server) brokenServerOutcome(name string) string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.upstreams[name] != nil || s.configServers[name] {
		return "keeping the config it last loaded"
	}
	return "skipping the server"
}

func (s *Server) brokenProjectionsOutcome(name string) string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.projections[name] != nil {
		return "keeping the server's previous projections"
	}
	return "the server has no projections until the file loads"
}

func serverProjections(servers []config.ServerConfig) map[string]map[string]*config.ProjectionConfig {
	projections := make(map[string]map[string]*config.ProjectionConfig, len(servers))
	for _, sc := range servers {
		if sc.Projections != nil {
			projections[sc.Name] = sc.Projections
		}
	}
	return projections
}

func keepsLiveProjections(servers config.Servers, name string) bool {
	sc, loaded := servers.Find(name)
	return servers.IsBroken(name) || (loaded && sc.ProjectionsErr != nil)
}

func (s *Server) replaceProjections(projections map[string]map[string]*config.ProjectionConfig, servers config.Servers) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	for name, live := range s.projections {
		if keepsLiveProjections(servers, name) {
			projections[name] = live
		}
	}
	s.projections = projections
}

func (s *Server) reapplyAliases() {
	s.serverOpMu.Lock()
	defer s.serverOpMu.Unlock()

	for _, u := range s.snapshotUpstreams() {
		if u.lastDefs == nil {
			continue
		}
		s.reg.ReplaceServerTools(registry.ServerParams{
			Name:            u.cfg.Name,
			Defs:            u.lastDefs,
			Perm:            u.cfg.Permissions,
			AliasByToolName: s.currentAliasesFor(u.cfg.Name),
		})
	}
}

func projectionCounts(projections map[string]map[string]*config.ProjectionConfig) map[string]int {
	counts := make(map[string]int, len(projections))
	for serverName, tools := range projections {
		counts[serverName] = len(tools)
	}
	return counts
}
