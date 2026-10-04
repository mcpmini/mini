package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

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
	if !config.ValidToolName.MatchString(p.Tool) {
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

	if err := s.checkSavedProjectionsLoad(p.ServerName); err != nil {
		return nil, err
	}
	prev := s.storeServerProjection(p.ServerName, p.Tool, p.Projection)
	if err := s.persistProjectionsLocked(p.ServerName); err != nil {
		s.restoreServerProjection(p.ServerName, p.Tool, prev)
		return nil, fmt.Errorf("set_projection: persistence failed: %w", err)
	}
	return map[string]any{"ok": true, "scope": "server", "tool": toolFullName(p.ServerName, visibleTool)}, nil
}

func (s *Server) checkSavedProjectionsLoad(serverName string) error {
	sc, err := config.LoadServer(s.configDir, serverName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err == nil && sc.ProjectionsErr != nil {
		err = sc.ProjectionsErr.Err
	}
	if err != nil {
		return fmt.Errorf("set_projection: %s's saved config fails to load (%w), so saving could replace rules on disk; fix the file, or pass session_only", serverName, err)
	}
	return nil
}

func (s *Server) storeServerProjection(serverName, tool string, projection *config.ProjectionConfig) *config.ProjectionConfig {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.projections[serverName] == nil {
		s.projections[serverName] = make(map[string]*config.ProjectionConfig)
	}
	prev := s.projections[serverName][tool]
	s.projections[serverName][tool] = preserveAlias(projection, prev)
	return prev
}

// preserveAlias carries the tool's existing Alias forward onto the new
// projection. The alias is admin-configured, not part of set_projection's
// agent-facing surface — without this, set_projection silently drops the
// alias from the persisted config, and it disappears on the next reload.
func preserveAlias(projection, prev *config.ProjectionConfig) *config.ProjectionConfig {
	if prev == nil || prev.Alias == "" {
		return projection
	}
	if projection == nil {
		return &config.ProjectionConfig{Alias: prev.Alias}
	}
	if projection.Alias == "" {
		projection.Alias = prev.Alias
	}
	return projection
}

func (s *Server) restoreServerProjection(serverName, tool string, prev *config.ProjectionConfig) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if prev != nil {
		s.projections[serverName][tool] = prev
		return
	}
	delete(s.projections[serverName], tool)
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
		s.logger.Warn("projections fail to load, "+s.brokenProjectionsOutcome(servers, se.ServerName), "server", se.ServerName, "path", se.Path, "err", se.Err)
	}
}

func (s *Server) brokenServerOutcome(name string) string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.upstreams[name] != nil || s.configServers[name] {
		return "keeping the server as it was"
	}
	return "skipping the server"
}

func (s *Server) brokenProjectionsOutcome(servers config.Servers, name string) string {
	if !servers.IsEnabled(name) {
		return "the server is disabled"
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.projections[name] != nil {
		return "keeping the server's previous projections"
	}
	return "the server runs without projections"
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

func (s *Server) persistProjectionsLocked(serverName string) error {
	if err := validateServerName(serverName); err != nil {
		return err
	}
	b, err := s.marshalServerProjections(serverName)
	if err != nil {
		return err
	}
	path := config.ProjectionPath(s.configDir, serverName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

func (s *Server) marshalServerProjections(serverName string) ([]byte, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return yaml.Marshal(s.projections[serverName])
}
