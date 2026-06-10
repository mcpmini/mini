package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/pipes"
	"github.com/mcpmini/mini/internal/registry"
)

func (s *Server) executePipe(ctx context.Context, entry *registry.ToolEntry, inputs map[string]any, session *Session) (any, error) {
	cp := s.getPipe(entry.Name)
	if cp == nil {
		return nil, fmt.Errorf("pipe not found: %s", entry.Name)
	}
	caller := s.makePipeCaller(ctx, session)
	return cp.Execute(ctx, inputs, caller), nil
}

func (s *Server) makePipeCaller(ctx context.Context, session *Session) pipes.CallerFunc {
	return func(callCtx context.Context, server, tool string, args map[string]any) (json.RawMessage, error) {
		return s.callRaw(callCtx, server, tool, args, session)
	}
}

// MakeRawCaller returns a CallerFunc backed by the server's upstream connections.
// Uses a fresh session so callers outside the MCP session lifecycle can invoke upstreams.
func (s *Server) MakeRawCaller(ctx context.Context) pipes.CallerFunc {
	session := newSession()
	return func(callCtx context.Context, server, tool string, args map[string]any) (json.RawMessage, error) {
		return s.callRaw(callCtx, server, tool, args, session)
	}
}

func (s *Server) callRaw(ctx context.Context, server, tool string, args map[string]any, session *Session) (json.RawMessage, error) {
	upstream, err := s.getUpstream(server)
	if err != nil {
		return nil, err
	}
	raw, _, toolErr := s.dispatchRaw(ctx, upstream, tool, args, session)
	return raw, toolErr
}

func (s *Server) getPipe(name string) *pipes.CompiledPipe {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.compiledPipes[name]
}

// AddPipes registers compiled pipes with the server and the registry.
func (s *Server) AddPipes(compiled []*pipes.CompiledPipe) {
	s.mu.Lock()
	for _, cp := range compiled {
		s.compiledPipes[cp.Config.Name] = cp
	}
	s.mu.Unlock()
	cfgs := make([]config.PipeConfig, 0, len(compiled))
	for _, cp := range compiled {
		cfgs = append(cfgs, cp.Config)
	}
	s.reg.AddPipes(cfgs, s.reg.PermLookup)
}
