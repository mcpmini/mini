package registry

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type Registry struct {
	mu       sync.RWMutex
	tools    map[string]*ToolEntry // open + protected tools
	hidden   map[string]*ToolEntry // hidden tools (excluded from Lookup)
	byServer map[string][]*ToolEntry
}

func New() *Registry {
	return &Registry{
		tools:    make(map[string]*ToolEntry),
		hidden:   make(map[string]*ToolEntry),
		byServer: make(map[string][]*ToolEntry),
	}
}

type ServerParams struct {
	Name    string
	Defs    []transport.ToolDefinition
	Perm    *config.PermissionsConfig
	Aliases map[string]string // realToolName → aliasName; nil means no aliases
}

func (r *Registry) AddServer(p ServerParams) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addServerLocked(p)
}

func (r *Registry) addServerLocked(p ServerParams) {
	realNames := realToolNames(p.Defs)
	visible, reverted := resolveVisibleNames(p.Defs, p.Aliases, realNames)
	seen := make(map[string]bool, len(p.Defs))
	for _, d := range p.Defs {
		if !config.ValidToolName.MatchString(d.Name) {
			continue
		}
		r.registerToolLocked(p, d, visible[d.Name], reverted[d.Name], seen)
	}
}

func (r *Registry) registerToolLocked(p ServerParams, d transport.ToolDefinition, visibleName string, reverted bool, seen map[string]bool) {
	if reverted {
		slog.Default().Warn("alias collides with existing tool name; using real name",
			"server", p.Name, "real", d.Name, "alias", p.Aliases[d.Name])
	}
	alias := ""
	if visibleName != d.Name {
		alias = visibleName
	}
	e := buildEntry(entryParams{server: p.Name, def: d, perm: p.Perm, alias: alias})
	if seen[e.FullName] {
		slog.Default().Warn("duplicate tool name from upstream; skipping", "server", p.Name, "tool", d.Name)
		return
	}
	seen[e.FullName] = true
	r.insertEntryLocked(p.Name, e)
}

func realToolNames(defs []transport.ToolDefinition) map[string]bool {
	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		names[d.Name] = true
	}
	return names
}

// resolveVisibleNames maps each real tool name to its visible name, reverting
// to the real name for tools whose alias collides with another tool's real
// name or with another tool's alias (symmetric: no "first one wins"). reverted
// reports the real names that were reverted due to such a collision.
func resolveVisibleNames(defs []transport.ToolDefinition, aliases map[string]string, realNames map[string]bool) (visible map[string]string, reverted map[string]bool) {
	claim := map[string][]string{}
	visible = make(map[string]string, len(defs))
	reverted = make(map[string]bool)
	for _, d := range defs {
		vis := d.Name
		if a := aliases[d.Name]; a != "" && config.ValidToolName.MatchString(a) {
			if realNames[a] {
				reverted[d.Name] = true
			} else {
				vis = a
			}
		}
		visible[d.Name] = vis
		claim[vis] = append(claim[vis], d.Name)
	}
	revertAliasCollisions(visible, claim, reverted)
	return visible, reverted
}

func revertAliasCollisions(visible map[string]string, claim map[string][]string, reverted map[string]bool) {
	for real, vis := range visible {
		if vis != real && len(claim[vis]) > 1 {
			visible[real] = real
			reverted[real] = true
		}
	}
}

type entryParams struct {
	server string
	def    transport.ToolDefinition
	perm   *config.PermissionsConfig
	alias  string
}

func buildEntry(p entryParams) *ToolEntry {
	visibleName, upstreamTool := p.def.Name, ""
	if p.alias != "" {
		visibleName, upstreamTool = p.alias, p.def.Name
	}
	full := p.server + "." + visibleName
	return &ToolEntry{
		Server:        p.server,
		Name:          visibleName,
		FullName:      full,
		FullNameLower: strings.ToLower(full),
		Description:   p.def.Description,
		DescLower:     strings.ToLower(p.def.Description),
		InputSchema:   p.def.InputSchema,
		Permission:    resolvePermission(p.def.Name, p.perm),
		ReadOnly:      p.def.ReadOnly,
		UpstreamTool:  upstreamTool,
	}
}

func (r *Registry) insertEntryLocked(server string, entry *ToolEntry) {
	if entry.Permission == config.PermHidden {
		r.hidden[entry.FullName] = entry
		return
	}
	r.tools[entry.FullName] = entry
	r.byServer[server] = append(r.byServer[server], entry)
}

// AddAction registers a virtual tool. It uses the target tool's permission
// unless overridden by ac.Permission. If the target tool isn't in the registry
// yet, the action is registered with PermOpen as a fallback.
func (r *Registry) AddAction(ac config.ActionConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	target := ac.Server + "." + ac.Tool
	if _, ok := r.targetPermissionLocked(target); !ok {
		slog.Default().Warn("action target tool not found in registry; will fail at call time", "action", ac.Server+"."+ac.Name, "target", target)
	}
	entry := r.buildActionEntry(ac)
	r.insertActionEntryLocked(ac.Server, entry)
}

func (r *Registry) buildActionEntry(ac config.ActionConfig) *ToolEntry {
	full := ac.Server + "." + ac.Name
	targetTool := ac.Tool
	if target, ok := r.tools[ac.Server+"."+ac.Tool]; ok && target.UpstreamTool != "" {
		targetTool = target.UpstreamTool
	}
	return &ToolEntry{
		Server:        ac.Server,
		Name:          ac.Name,
		FullName:      full,
		FullNameLower: strings.ToLower(full),
		Description:   ac.Description,
		DescLower:     strings.ToLower(ac.Description),
		Permission:    r.actionPermission(ac),
		TargetServer:  ac.Server,
		TargetTool:    targetTool,
		DefaultArgs:   ac.DefaultArgs,
	}
}

func (r *Registry) actionPermission(ac config.ActionConfig) config.PermissionLevel {
	switch config.PermissionLevel(ac.Permission) {
	case config.PermOpen, config.PermProtected, config.PermHidden:
		return config.PermissionLevel(ac.Permission)
	case "":
	default:
		slog.Default().Warn("invalid action permission; defaulting to protected", "action", ac.Server+"."+ac.Name, "permission", ac.Permission)
		return config.PermProtected
	}
	if perm, ok := r.targetPermissionLocked(ac.Server + "." + ac.Tool); ok {
		return perm
	}
	return config.PermOpen
}

func (r *Registry) targetPermissionLocked(fullName string) (config.PermissionLevel, bool) {
	if target, ok := r.tools[fullName]; ok {
		return target.Permission, true
	}
	if target, ok := r.hidden[fullName]; ok {
		return target.Permission, true
	}
	server, real, found := strings.Cut(fullName, ".")
	if !found {
		return "", false
	}
	return r.permissionByUpstreamToolLocked(server, real)
}

// permissionByUpstreamToolLocked finds the permission of a tool by its
// upstream (real) name, searching both visible and hidden entries — an
// aliased tool is keyed by its alias, not its real name, in both maps.
func (r *Registry) permissionByUpstreamToolLocked(server, real string) (config.PermissionLevel, bool) {
	for _, e := range r.tools {
		if e.Server == server && e.UpstreamTool == real {
			return e.Permission, true
		}
	}
	for _, e := range r.hidden {
		if e.Server == server && e.UpstreamTool == real {
			return e.Permission, true
		}
	}
	return "", false
}

func (r *Registry) insertActionEntryLocked(server string, entry *ToolEntry) {
	delete(r.tools, entry.FullName)
	delete(r.hidden, entry.FullName)
	r.removeServerEntry(server, entry.FullName)
	if entry.Permission == config.PermHidden {
		r.hidden[entry.FullName] = entry
		return
	}
	r.tools[entry.FullName] = entry
	r.upsertServerEntry(server, entry)
}

func (r *Registry) removeServerEntry(server, fullName string) {
	entries := r.byServer[server]
	for i, e := range entries {
		if e.FullName == fullName {
			r.byServer[server] = append(entries[:i], entries[i+1:]...)
			return
		}
	}
}

func (r *Registry) upsertServerEntry(server string, entry *ToolEntry) {
	entries := r.byServer[server]
	for i, e := range entries {
		if e.FullName == entry.FullName {
			entries[i] = entry
			return
		}
	}
	r.byServer[server] = append(entries, entry)
}

func (r *Registry) RemoveServer(serverName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeServerLocked(serverName)
}

func (r *Registry) removeServerLocked(serverName string) {
	for _, e := range r.byServer[serverName] {
		delete(r.tools, e.FullName)
	}
	for key, e := range r.hidden {
		if e.Server == serverName {
			delete(r.hidden, key)
		}
	}
	delete(r.byServer, serverName)
}

// ReplaceServer atomically removes the server's existing tools and registers the
// new set. Callers outside this package (reconnect, registerUpstream) must use
// this instead of separate Remove+Add calls to avoid a window where tools are absent.
func (r *Registry) ReplaceServer(p ServerParams) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeServerLocked(p.Name)
	r.addServerLocked(p)
}

func (r *Registry) Lookup(fullName string) (*ToolEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.tools[fullName]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", fullName)
	}
	return e, nil
}

func (r *Registry) AllFull() []*ToolEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*ToolEntry, 0, len(r.tools))
	for _, e := range r.tools {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FullName < out[j].FullName })
	return out
}

func (r *Registry) All() []CompactEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]CompactEntry, 0, len(r.tools))
	for _, e := range r.tools {
		out = append(out, e.Compact())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AllWithHidden includes tools marked permission="hidden" that All() omits.
func (r *Registry) AllWithHidden() []CompactEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]CompactEntry, 0, len(r.tools)+len(r.hidden))
	for _, e := range r.tools {
		out = append(out, e.Compact())
	}
	for _, e := range r.hidden {
		out = append(out, e.Compact())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) Search(query string) []CompactEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	q := strings.ToLower(query)
	var out []CompactEntry
	for _, e := range r.tools {
		if strings.Contains(e.FullNameLower, q) || strings.Contains(e.DescLower, q) {
			out = append(out, e.Compact())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *Registry) ServerNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.byServer))
	for name := range r.byServer {
		names = append(names, name)
	}
	return names
}

func (r *Registry) ToolCount(serverName string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byServer[serverName])
}

func resolvePermission(toolName string, perm *config.PermissionsConfig) config.PermissionLevel {
	return perm.LevelFor(toolName)
}
