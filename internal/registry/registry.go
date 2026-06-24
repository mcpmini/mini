package registry

import (
	"encoding/json"
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
	Name            string
	Defs            []transport.ToolDefinition
	Perm            *config.PermissionsConfig
	AliasByToolName map[string]string
}

func (r *Registry) AddServer(p ServerParams) {
	if config.IsReservedServerName(p.Name) {
		slog.Default().Warn("ignoring server with reserved name", "server", p.Name)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addServerLocked(p)
}

func (r *Registry) PermLookup(server, tool string) (config.PermissionLevel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	full := server + "." + tool
	if e, ok := r.tools[full]; ok {
		return e.Permission, true
	}
	if e, ok := r.hidden[full]; ok {
		return e.Permission, true
	}
	return "", false
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
		Server:        p.server,
		ToolName:      name,
		FullName:      full,
		FullNameLower: strings.ToLower(full),
		Description:   p.def.Description,
		DescLower:     strings.ToLower(p.def.Description),
		InputSchema:   p.def.InputSchema,
		Annotations: p.def.Annotations,
		Permission:  resolvePermission(p.def.Name, p.perm),
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
	if target, ok := r.entryByFullNameLocked(ac.Server + "." + ac.Tool); ok && target.ToolName.Alias != "" {
		targetTool = target.ToolName.UpstreamName
	}
	return &ToolEntry{
		Server:        ac.Server,
		ToolName:      ToolName{UpstreamName: ac.Name},
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

// entryByFullNameLocked looks up an entry by its visible full name, searching
// both the open/protected and hidden maps.
func (r *Registry) entryByFullNameLocked(fullName string) (*ToolEntry, bool) {
	if e, ok := r.tools[fullName]; ok {
		return e, true
	}
	e, ok := r.hidden[fullName]
	return e, ok
}

func (r *Registry) targetPermissionLocked(fullName string) (config.PermissionLevel, bool) {
	if target, ok := r.entryByFullNameLocked(fullName); ok {
		return target.Permission, true
	}
	server, upstreamTool, found := strings.Cut(fullName, ".")
	if !found {
		return "", false
	}
	return r.permissionByUpstreamToolLocked(server, upstreamTool)
}

func (r *Registry) permissionByUpstreamToolLocked(server, upstreamTool string) (config.PermissionLevel, bool) {
	for _, e := range r.tools {
		if e.Server == server && e.ToolName.Alias != "" && e.ToolName.UpstreamName == upstreamTool {
			return e.Permission, true
		}
	}
	// Aliased tools are keyed by their alias, not their real name, so hidden
	// aliased tools won't appear in r.tools — must scan r.hidden too.
	for _, e := range r.hidden {
		if e.Server == server && e.ToolName.Alias != "" && e.ToolName.UpstreamName == upstreamTool {
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

// PermLookupFunc resolves the permission level for a given server+tool.
// Returns ("", false) when the server is not connected.
type PermLookupFunc func(server, tool string) (config.PermissionLevel, bool)

// AddPipes registers pipe virtual tools under the reserved "user" server.
func (r *Registry) AddPipes(pipes []config.PipeConfig, lookup PermLookupFunc) {
	entries := make([]*ToolEntry, 0, len(pipes))
	for _, pipe := range pipes {
		p := pipe
		entries = append(entries, r.buildPipeEntry(&p, lookup))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range entries {
		r.insertEntryLocked(config.UserServerName, entry)
	}
}

func (r *Registry) buildPipeEntry(pipe *config.PipeConfig, lookup PermLookupFunc) *ToolEntry {
	full := config.UserServerName + "." + pipe.Name
	schema := buildPipeInputSchema(pipe.Inputs)
	return &ToolEntry{
		Server:        config.UserServerName,
		ToolName:      ToolName{UpstreamName: pipe.Name},
		FullName:      full,
		FullNameLower: strings.ToLower(full),
		Description:   pipe.Description,
		DescLower:     strings.ToLower(pipe.Description),
		InputSchema:   schema,
		Permission:    computePipePermission(pipe, lookup),
		Pipe:          pipe,
	}
}

func buildPipeInputSchema(inputs map[string]config.InputSchema) json.RawMessage {
	if len(inputs) == 0 {
		return json.RawMessage(`{"type":"object"}`)
	}
	properties := make(map[string]any, len(inputs))
	var required []string
	for name, schema := range inputs {
		prop := map[string]any{"type": schema.Type}
		if schema.Description != "" {
			prop["description"] = schema.Description
		}
		properties[name] = prop
		if schema.Required {
			required = append(required, name)
		}
	}
	obj := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		obj["required"] = required
	}
	b, _ := json.Marshal(obj)
	return json.RawMessage(b)
}

func computePipePermission(pipe *config.PipeConfig, lookup PermLookupFunc) config.PermissionLevel {
	inherited := inheritedPipePermission(pipe, lookup)
	switch override := config.PermissionLevel(pipe.Permission); override {
	case config.PermProtected, config.PermHidden:
		return override
	case config.PermOpen:
		// An explicit "open" cannot downgrade a pipe whose steps require
		// protection — that would silently disable perm_call gating for
		// the protected step.
		if inherited == config.PermProtected {
			return config.PermProtected
		}
		return config.PermOpen
	default:
		return inherited
	}
}

func inheritedPipePermission(pipe *config.PipeConfig, lookup PermLookupFunc) config.PermissionLevel {
	perm := config.PermOpen
	for _, step := range pipe.Steps {
		if step.Server == "" {
			continue
		}
		stepPerm, ok := lookup(step.Server, step.Tool)
		if !ok {
			return config.PermProtected
		}
		if stepPerm == config.PermProtected || stepPerm == config.PermHidden {
			perm = config.PermProtected
		}
	}
	return perm
}
