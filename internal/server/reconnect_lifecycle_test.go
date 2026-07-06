//go:build test

package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestReconnectSwapAndShutdownLinearizations(t *testing.T) {
	t.Run("reconnect_installs_before_shutdown", func(t *testing.T) {
		u := newLifecycleUpstream(t, newCountedConn("old"))
		candidate := newCountedConn("candidate")

		swap := swapReconnectConn(u, candidate)
		if !swap.ok {
			t.Fatal("swapReconnectConn() = not ok")
		}
		old := swap.old.(*countedConn)
		if got := old.closeCount(); got != 0 {
			t.Fatalf("old close count before caller close = %d, want 0", got)
		}
		swap.old.Close()
		if got := candidate.closeCount(); got != 0 {
			t.Fatalf("candidate close count before shutdown = %d, want 0", got)
		}

		u.shutdownAndClose()
		assertLifecycleClosedExactlyOnce(t, old, candidate)
		assertNoInstalledConn(t, u)
		u.shutdownAndClose()
		assertLifecycleClosedExactlyOnce(t, old, candidate)
	})

	t.Run("shutdown_wins_before_reconnect_install", func(t *testing.T) {
		old := newCountedConn("old")
		u := newLifecycleUpstream(t, old)
		candidate := newCountedConn("candidate")

		u.shutdownAndClose()
		swap := swapReconnectConn(u, candidate)
		if swap.ok {
			t.Fatal("swapReconnectConn() = ok after shutdown")
		}

		assertLifecycleClosedExactlyOnce(t, old, candidate)
		assertNoInstalledConn(t, u)
		u.shutdownAndClose()
		assertLifecycleClosedExactlyOnce(t, old, candidate)
	})
}

func newLifecycleUpstream(t *testing.T, conn transport.Connection) *upstreamServer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	u := &upstreamServer{
		cfg:    config.ServerConfig{Name: "svc"},
		ctx:    ctx,
		cancel: cancel,
		clock:  clock.NewFake(),
	}
	u.initConn(conn)
	return u
}

type countedConn struct {
	name   string
	mu     sync.Mutex
	closed int
}

func newCountedConn(name string) *countedConn {
	return &countedConn{name: name}
}

func (c *countedConn) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

func (c *countedConn) ListTools(context.Context) ([]transport.ToolDefinition, error) {
	return nil, nil
}

func (c *countedConn) Health(context.Context) error { return nil }

func (c *countedConn) Close() error {
	c.mu.Lock()
	c.closed++
	c.mu.Unlock()
	return nil
}

func (c *countedConn) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func assertLifecycleClosedExactlyOnce(t *testing.T, conns ...*countedConn) {
	t.Helper()
	for _, conn := range conns {
		if got := conn.closeCount(); got != 1 {
			t.Fatalf("%s close count = %d, want 1", conn.name, got)
		}
	}
}

func assertNoInstalledConn(t *testing.T, u *upstreamServer) {
	t.Helper()
	if state := u.connState(); state != nil {
		t.Fatalf("connState() = %#v, want nil", state)
	}
	if gen := u.currentConnGen(); gen != 0 {
		t.Fatalf("currentConnGen() = %d, want 0", gen)
	}
}
