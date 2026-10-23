//go:build test

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type toolListConnection struct {
	*transport.FakeConnection
	calls atomic.Int32
	list  func(context.Context, int32) (transport.ToolList, error)
}

func (c *toolListConnection) ListTools(ctx context.Context) (transport.ToolList, error) {
	return c.list(ctx, c.calls.Add(1))
}

func newToolListConnection(list func(context.Context, int32) (transport.ToolList, error)) *toolListConnection {
	return &toolListConnection{FakeConnection: &transport.FakeConnection{}, list: list}
}

func toolListNamed(name string, freshness transport.Freshness) transport.ToolList {
	return transport.ToolList{
		Tools:     []transport.ToolDefinition{{Name: name, InputSchema: json.RawMessage(`{}`)}},
		Freshness: freshness,
	}
}

func oldThenNew(fetch int32) string {
	if fetch == 1 {
		return "old"
	}
	return "new"
}

func newToolListTestServer(t *testing.T) (*Server, *clock.Fake) {
	t.Helper()
	s := newInternalTestServer(t)
	fc := clock.NewFake()
	s.clock = fc
	return s, fc
}

func addToolListConnection(t *testing.T, s *Server, c *toolListConnection) *upstreamServer {
	t.Helper()
	if err := s.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, c); err != nil {
		t.Fatal(err)
	}
	return s.snapshotUpstreams()[0]
}

func proxyToolsList(t *testing.T, s *Server) string {
	t.Helper()
	result, err := s.handleToolsList(t.Context(), newSession(s.clock))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	return string(raw)
}

func TestToolListExpiryRefreshesEveryDiscoveryEntryPoint(t *testing.T) {
	for entry, read := range map[string]func(*Server) (any, error){
		"proxy": func(s *Server) (any, error) { return s.handleToolsList(context.Background(), newSession(s.clock)) },
		"list":  func(s *Server) (any, error) { return s.handleList(context.Background(), nil) },
		"search": func(s *Server) (any, error) {
			return s.handleList(context.Background(), json.RawMessage(`{"query":"new"}`))
		},
		"hidden": func(s *Server) (any, error) {
			return s.handleList(context.Background(), json.RawMessage(`{"hidden":true}`))
		},
		"detail": func(s *Server) (any, error) {
			return s.handleList(context.Background(), json.RawMessage(`{"tool":"svc.new","detail":true}`))
		},
	} {
		t.Run(entry, func(t *testing.T) {
			s, fc := newToolListTestServer(t)
			c := newToolListConnection(func(_ context.Context, n int32) (transport.ToolList, error) {
				return toolListNamed(oldThenNew(n), transport.FreshUntil(fc.Now().Add(time.Second))), nil
			})
			addToolListConnection(t, s, c)
			if got := proxyToolsList(t, s); !strings.Contains(got, "svc__old") || c.calls.Load() != 1 {
				t.Fatalf("read before expiry = %s after %d fetches; want the installed list", got, c.calls.Load())
			}
			fc.Advance(time.Second)
			result, err := read(s)
			raw, _ := json.Marshal(result)
			if err != nil || !strings.Contains(string(raw), "new") || strings.Contains(string(raw), "old") ||
				c.calls.Load() != 2 {
				t.Fatalf("%s after expiry = %s, %v after %d fetches; want one refetch", entry, raw, err, c.calls.Load())
			}
		})
	}
}

func TestToolListZeroTTLFetchesOncePerDiscoveryRead(t *testing.T) {
	s, fc := newToolListTestServer(t)
	c := newToolListConnection(func(context.Context, int32) (transport.ToolList, error) {
		return toolListNamed("tool", transport.FreshUntil(fc.Now())), nil
	})
	addToolListConnection(t, s, c)
	for want := int32(2); want <= 4; want++ {
		proxyToolsList(t, s)
		if c.calls.Load() != want {
			t.Fatalf("fetches = %d after %d reads; want one per read", c.calls.Load(), want-1)
		}
	}
}

func TestToolListCancelledReaderLeavesTheSharedRefreshRunning(t *testing.T) {
	s, fc := newToolListTestServer(t)
	entered, release := make(chan struct{}), make(chan struct{})
	c := newToolListConnection(func(ctx context.Context, n int32) (transport.ToolList, error) {
		if n == 2 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return transport.ToolList{}, ctx.Err()
			}
		}
		return toolListNamed(fmt.Sprint(n), transport.FreshUntil(fc.Now())), nil
	})
	u := addToolListConnection(t, s, c)
	done := u.refresher.refreshIfStale(fc.Now())
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.awaitCurrentToolLists(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read = %v, want context.Canceled", err)
	}
	if other := u.refresher.refreshIfStale(fc.Now()); other != done {
		t.Fatal("a later reader did not join the refresh the cancelled reader left")
	}
	close(release)
	<-done
	if c.calls.Load() != 2 {
		t.Fatalf("fetches = %d, want the one shared refresh", c.calls.Load())
	}
}

func TestToolListExcludedEntryPointsDoNotFetch(t *testing.T) {
	s, fc := newToolListTestServer(t)
	c := newToolListConnection(func(context.Context, int32) (transport.ToolList, error) {
		return toolListNamed("old", transport.FreshUntil(fc.Now())), nil
	})
	addToolListConnection(t, s, c)
	compact := newSession(fc)
	compact.mode.Store(int32(transport.ToolModeCompact))
	if _, err := s.handleToolsList(t.Context(), compact); err != nil {
		t.Fatal(err)
	}
	detail, err := s.handleList(t.Context(), json.RawMessage(`{"tool":"svc.bad tool","detail":true}`))
	if got, _ := json.Marshal(detail); err != nil || !strings.Contains(string(got), "not_found") {
		t.Fatalf("malformed detail = %s, %v; want a not_found result", got, err)
	}
	s.cfg.DisableListHidden = true
	if _, err := s.handleList(t.Context(), json.RawMessage(`{"hidden":true}`)); !errors.Is(err, errListHiddenDisabled) {
		t.Fatalf("disabled hidden listing = %v, want %v", err, errListHiddenDisabled)
	}
	if c.calls.Load() != 1 {
		t.Fatalf("excluded entry points fetched %d times", c.calls.Load()-1)
	}
}
