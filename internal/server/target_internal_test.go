//go:build test

package server

import (
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

func TestMissingToolTargetCannotDispatch(t *testing.T) {
	srv := newInstallTestServer(t)
	fake := &transport.FakeConnection{Tools: []transport.ToolDefinition{{Name: "run"}}}
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, fake); err != nil {
		t.Fatal(err)
	}
	entry, err := srv.reg.Lookup("svc.run")
	if err != nil {
		t.Fatal(err)
	}
	entry.Target = nil
	assertMissingTargetNotDispatched(t, srv, entry)
	if fake.LastParams != nil {
		t.Errorf("missing target reached upstream: %s", fake.LastParams)
	}
}

func assertMissingTargetNotDispatched(t *testing.T, srv *Server, entry *registry.ToolEntry) {
	t.Helper()
	session := newSession(srv.clock)
	t.Cleanup(session.Close)
	p := executeParams{Server: "svc", Tool: "run"}
	if _, err := srv.callUpstream(t.Context(), p, entry, session); err == nil {
		t.Error("compact dispatch accepted a missing target")
	}
	if _, err := srv.proxyCallUpstream(
		t.Context(),
		proxyCallParams{Server: "svc", Tool: "run", Entry: entry, Session: session},
	); err == nil {
		t.Error("proxy dispatch accepted a missing target")
	}
}

func TestUnsupportedToolTargetCannotDispatch(t *testing.T) {
	var target *registry.ActionTarget
	entry := &registry.ToolEntry{Target: target}
	if _, err := resolveTarget(executeParams{}, entry); err == nil {
		t.Error("typed nil action target accepted")
	}
	if _, err := resolveTarget(executeParams{}, nil); err == nil {
		t.Error("missing tool entry accepted")
	}
}
