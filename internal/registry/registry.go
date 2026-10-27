package registry

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type Registry struct {
	mu       sync.RWMutex
	tools    map[string]*ToolEntry // visible tools
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
	Name            string
	Defs            []transport.ToolDefinition
	Perm            *config.PermissionsConfig
	AliasByToolName map[string]string
}

func (r *Registry) AddServer(p ServerParams) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addServerLocked(p)
}

func (r *Registry) addServerLocked(p ServerParams) {
	resolution := ResolveAliases(toolDefNames(p.Defs), p.AliasByToolName)
	seen := make(map[string]bool, len(p.Defs))
	for _, def := range p.Defs {
		if !config.ValidToolName.MatchString(def.Name) {
			continue
		}
		if resolution.WasDropped(def.Name) {
			slog.Default().Warn("alias collides; using real name",
				"server", p.Name, "tool", def.Name, "alias", p.AliasByToolName[def.Name])
		}
		entry := buildEntry(entryParams{server: p.Name, def: def, perm: p.Perm, alias: resolution.AliasFor(def.Name)})
		if seen[entry.FullName] {
			slog.Default().Warn("duplicate tool name from upstream; skipping", "server", p.Name, "tool", def.Name)
			continue
		}
		seen[entry.FullName] = true
		r.insertEntryLocked(p.Name, entry)
	}
}

func toolDefNames(defs []transport.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

type entryParams struct {
	server string
	def    transport.ToolDefinition
	perm   *config.PermissionsConfig
	alias  string
}

func buildEntry(p entryParams) *ToolEntry {
	name := ToolName{UpstreamName: p.def.Name, Alias: p.alias}
	full := p.server + "." + name.Name()
	return &ToolEntry{
		Server:   p.server,
		ToolName: name,
		FullName: full,
		Def:      p.def,
		Hidden:   p.perm.Hides(p.def.Name),
	}
}

func (r *Registry) insertEntryLocked(server string, entry *ToolEntry) {
	if entry.Hidden {
		r.hidden[entry.FullName] = entry
		return
	}
	r.tools[entry.FullName] = entry
	r.byServer[server] = append(r.byServer[server], entry)
}

// entryByFullNameLocked looks up an entry by its visible full name, searching
// both the visible and hidden maps.
func (r *Registry) entryByFullNameLocked(fullName string) (*ToolEntry, bool) {
	if e, ok := r.tools[fullName]; ok {
		return e, true
	}
	e, ok := r.hidden[fullName]
	return e, ok
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

func (r *Registry) LookupWithHidden(fullName string) (*ToolEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.entryByFullNameLocked(fullName)
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
