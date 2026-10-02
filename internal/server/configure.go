package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
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
		return s.reloadProjections(), nil
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

	prev := s.storeServerProjection(p.ServerName, p.Tool, p.Projection)
	if err := s.persistProjectionsLocked(p.ServerName); err != nil {
		s.restoreServerProjection(p.ServerName, p.Tool, prev)
		return nil, fmt.Errorf("set_projection: persistence failed: %w", err)
	}
	return map[string]any{"ok": true, "scope": "server", "tool": toolFullName(p.ServerName, visibleTool)}, nil
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

func (s *Server) applyReload() (config.LoadProjectionsResult, map[string]int) {
	// Hold persistMu for the entire load+replace so we don't interleave with a
	// concurrent set_projection that has already updated the in-memory map but
	// hasn't yet flushed to disk: without this lock, reload could wipe the
	// in-memory update and then set_projection would persist the wiped state.
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	load := config.LoadProjections(s.configDir)
	logProjectionLoadProblems(s.logger, load)
	fresh := projectionCounts(load.Projections)
	s.replaceProjections(load)
	s.reapplyAliases()
	return load, fresh
}

func (s *Server) reloadProjections() any {
	load, fresh := s.applyReload()
	return buildReloadResult(load, fresh)
}

func buildReloadResult(load config.LoadProjectionsResult, fresh map[string]int) map[string]any {
	ok := len(load.SourceErrors) == 0 && len(load.SkippedServers) == 0
	result := map[string]any{
		"ok":      ok,
		"loaded":  fresh,
		"skipped": skippedNames(load.SkippedServers),
	}
	if len(load.SourceErrors) > 0 {
		result["source_errors"] = sourceErrorPaths(load.SourceErrors)
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

func logProjectionLoadProblems(logger *slog.Logger, load config.LoadProjectionsResult) {
	for _, se := range load.SourceErrors {
		logger.Warn("projection reload: source error", "path", se.Path, "err", se.Err)
	}
	for name, err := range load.SkippedServers {
		logger.Warn("projection reload: skipped server", "server", name, "err", err)
	}
}

func skippedNames(skipped map[string]error) []string {
	if len(skipped) == 0 {
		return []string{}
	}
	return slices.Sorted(maps.Keys(skipped))
}

func (s *Server) replaceProjections(load config.LoadProjectionsResult) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.carryOverPreviousProjectionsLocked(load)
	s.projections = load.Projections
}

func (s *Server) carryOverPreviousProjectionsLocked(load config.LoadProjectionsResult) {
	for name, live := range s.projections {
		if load.KeepsPreviousProjection(name) {
			load.Projections[name] = live
		}
	}
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
	report := map[string]any{
		"servers":     servers,
		"store":       map[string]any{"files": fileCount, "used_mb": float64(usedBytes) / (1024 * 1024)},
		"projections": projInfo,
		"sessions":    s.sessions.aggregateMetrics(),
	}
	if broken := s.brokenServerFiles(); len(broken) > 0 {
		report["broken_servers"] = broken
	}
	return report
}

// brokenServerFiles reads the files again, so the agent sees why a server it expects isn't
// running, such as an environment variable unset where mini runs, and whether a fix has landed.
func (s *Server) brokenServerFiles() map[string]string {
	loaded, err := config.Load(s.configDir)
	if err != nil {
		return map[string]string{"config.yaml": err.Error()}
	}
	broken := make(map[string]string, len(loaded.Broken))
	for _, b := range loaded.Broken {
		broken[b.ServerName] = b.Err.Error()
	}
	return broken
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
