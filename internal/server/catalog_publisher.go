package server

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

type catalogPublisher struct {
	mu         sync.Mutex
	removalGen map[string]uint64
	server     *Server
}

func newCatalogPublisher(server *Server) *catalogPublisher {
	return &catalogPublisher{server: server, removalGen: make(map[string]uint64)}
}

func (p *catalogPublisher) snapshotRemovalGeneration(name string) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.removalGen[name]
}

func (p *catalogPublisher) installIfCurrent(sc config.ServerConfig, conn transport.Connection, tools []transport.ToolDefinition, gen uint64) (*upstreamServer, error) {
	p.mu.Lock()
	if p.removalGen[sc.Name] != gen {
		p.mu.Unlock()
		conn.Close()
		return nil, fmt.Errorf("server %q was removed during connection setup", sc.Name)
	}
	before := p.visibleSchemas()
	u := p.server.installUpstream(sc, conn, tools)
	changed := p.changed(before)
	p.mu.Unlock()
	p.notifyIfChanged(changed)
	return u, nil
}

func (p *catalogPublisher) replaceCurrent(u *upstreamServer, gen uint64, tools []transport.ToolDefinition) bool {
	p.mu.Lock()
	if !p.server.isCurrentConnectionGen(u, gen) {
		p.mu.Unlock()
		return false
	}
	before := p.visibleSchemas()
	u.lastDefs = tools
	p.server.reg.ReplaceServerTools(p.serverParams(u, tools))
	changed := p.changed(before)
	p.mu.Unlock()
	p.notifyIfChanged(changed)
	return true
}

func (p *catalogPublisher) reapplyAliases() {
	p.mu.Lock()
	before := p.visibleSchemas()
	for _, u := range p.server.snapshotUpstreams() {
		if u.lastDefs != nil {
			p.server.reg.ReplaceServerTools(p.serverParams(u, u.lastDefs))
		}
	}
	changed := p.changed(before)
	p.mu.Unlock()
	p.notifyIfChanged(changed)
}

func (p *catalogPublisher) remove(serverName string) {
	p.mu.Lock()
	before := p.visibleSchemas()
	p.removalGen[serverName]++
	if u := p.server.detachUpstream(serverName); u != nil {
		u.shutdownAndClose()
	}
	p.server.sessions.closeServerConnections(serverName)
	p.server.reg.RemoveServer(serverName)
	changed := p.changed(before)
	p.mu.Unlock()
	p.notifyIfChanged(changed)
}

func (p *catalogPublisher) serverParams(u *upstreamServer, tools []transport.ToolDefinition) registry.ServerParams {
	return registry.ServerParams{
		Name:            u.cfg.Name,
		Defs:            tools,
		Perm:            u.cfg.Permissions,
		AliasByToolName: p.server.currentAliasesFor(u.cfg.Name),
	}
}

func (p *catalogPublisher) visibleSchemas() []map[string]any {
	return buildProxyToolSchemas(p.server.reg.AllFull())
}

func (p *catalogPublisher) changed(before []map[string]any) bool {
	return !reflect.DeepEqual(before, p.visibleSchemas())
}

func (p *catalogPublisher) notifyIfChanged(changed bool) {
	if changed {
		p.server.notifyAllSessions()
	}
}
