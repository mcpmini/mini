//go:build test

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type toolListConnection struct {
	*transport.FakeConnection
	name  string
	calls atomic.Int32
	list  func(context.Context, int32) (transport.ToolList, error)
}

func (c *toolListConnection) ListTools(ctx context.Context) (transport.ToolList, error) {
	return c.list(ctx, c.calls.Add(1))
}

func newToolListConnection(
	name string,
	list func(context.Context, int32) (transport.ToolList, error),
) *toolListConnection {
	return &toolListConnection{FakeConnection: &transport.FakeConnection{}, name: name, list: list}
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
	if err := s.AddConnection(t.Context(), config.ServerConfig{Name: c.name}, c); err != nil {
		t.Fatal(err)
	}
	u, err := s.getUpstream(c.name)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func readToolsList(s *Server) string {
	result, err := s.handleToolsList(context.Background())
	raw, _ := json.Marshal(result)
	return fmt.Sprint(string(raw), err)
}

func TestToolListExpiryRefreshesEveryDiscoveryEntryPoint(t *testing.T) {
	for entry, read := range map[string]func(*Server) (any, error){
		"proxy": func(s *Server) (any, error) { return s.handleToolsList(context.Background()) },
	} {
		t.Run(entry, func(t *testing.T) {
			s, fc := newToolListTestServer(t)
			c := newToolListConnection("svc", func(_ context.Context, n int32) (transport.ToolList, error) {
				return toolListNamed(oldThenNew(n), transport.FreshUntil(fc.Now().Add(time.Second))), nil
			})
			addToolListConnection(t, s, c)
			if got := readToolsList(s); !strings.Contains(got, "svc__old") || c.calls.Load() != 1 {
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

func TestToolListReadersShareOneRefreshPerUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, fc := newToolListTestServer(t)
		release := make(chan struct{})
		connections := make([]*toolListConnection, 0, 2)
		for _, name := range []string{"one", "two"} {
			c := newToolListConnection(name, func(ctx context.Context, n int32) (transport.ToolList, error) {
				if n > 1 {
					<-release
				}
				return toolListNamed(oldThenNew(n), transport.FreshUntil(fc.Now().Add(time.Second))), nil
			})
			addToolListConnection(t, s, c)
			connections = append(connections, c)
		}
		fc.Advance(time.Second)
		var wg sync.WaitGroup
		results := make([]string, 10)
		for i := range results {
			wg.Go(func() { results[i] = readToolsList(s) })
		}
		synctest.Wait()
		close(release)
		wg.Wait()
		for _, c := range connections {
			if c.calls.Load() != 2 {
				t.Fatalf("fetches = %d, want one shared refresh per upstream", c.calls.Load())
			}
		}
		for _, got := range results {
			if !strings.Contains(got, "one__new") || !strings.Contains(got, "two__new") {
				t.Fatalf("a reader got %s; want both refreshed lists", got)
			}
		}
	})
}

func TestToolListCancelledReaderLeavesTheSharedRefreshRunning(t *testing.T) {
	s, fc := newToolListTestServer(t)
	entered, release := make(chan struct{}), make(chan struct{})
	c := newToolListConnection("svc", func(ctx context.Context, n int32) (transport.ToolList, error) {
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

func TestToolListDiscoveryReadsAnswerWithinTheWait(t *testing.T) {
	for _, tc := range []struct {
		name          string
		slowAnswer    time.Duration
		startupFirst  bool
		wantPublished bool
	}{
		{name: "hung upstreams"},
		{name: "slow upstreams publish after the read gives up", slowAnswer: discoveryWait + 15*time.Second, wantPublished: true},
		{name: "startup hold counts toward the wait", startupFirst: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// The startup hold runs on the server clock, so these use the real one, which synctest
				// makes virtual.
				s := newInternalTestServer(t)
				for _, name := range []string{"one", "two"} {
					addToolListConnection(
						t,
						s,
						newToolListConnection(name, func(ctx context.Context, n int32) (transport.ToolList, error) {
							if n == 1 {
								return toolListNamed("old", transport.FreshUntil(time.Now())), nil
							}
							if tc.slowAnswer == 0 {
								<-ctx.Done()
								return transport.ToolList{}, ctx.Err()
							}
							time.Sleep(tc.slowAnswer)
							return toolListNamed("new", transport.FreshUntil(time.Now().Add(time.Hour))), nil
						}),
					)
				}
				if tc.startupFirst {
					s.recordConfigServers([]config.ServerConfig{{Name: "connecting"}})
					s.openConnectWindow("connecting")
				}
				started := time.Now()
				got := readToolsList(s)
				if elapsed := time.Since(started); elapsed != discoveryWait {
					t.Fatalf("read waited %v, want one request-wide %v", elapsed, discoveryWait)
				}
				if !strings.Contains(got, "one__old") || !strings.Contains(got, "two__old") {
					t.Fatalf("read that gave up = %s; want the lists it had", got)
				}
				s.refreshWg.Wait()
				if got := readToolsList(s); tc.wantPublished != strings.Contains(got, "one__new") {
					t.Fatalf("read after the refresh ended = %s; want published=%v", got, tc.wantPublished)
				}
			})
		})
	}
}

func TestToolListShutdownReleasesDiscoveryReads(t *testing.T) {
	for name, shutdown := range map[string]func(*Server, *upstreamServer){
		"mini":     func(s *Server, _ *upstreamServer) { s.ReleaseStartupHolds() },
		"upstream": func(_ *Server, u *upstreamServer) { u.shutdown() },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, fc := newToolListTestServer(t)
				u := addToolListConnection(
					t,
					s,
					newToolListConnection("svc", func(ctx context.Context, n int32) (transport.ToolList, error) {
						if n == 2 {
							<-ctx.Done()
							return transport.ToolList{}, ctx.Err()
						}
						return toolListNamed("old", transport.FreshUntil(fc.Now())), nil
					}),
				)
				read := make(chan string, 1)
				go func() { read <- readToolsList(s) }()
				synctest.Wait()
				started := time.Now()
				shutdown(s, u)
				if got := <-read; time.Since(started) != 0 || !strings.Contains(got, "svc__old") {
					t.Fatalf(
						"read during %s shutdown = %s after %v; want the list it had at once",
						name,
						got,
						time.Since(started),
					)
				}
				s.refreshWg.Wait()
			})
		})
	}
}

func TestToolListLateRefreshCannotPublishAfterLifecycleChange(t *testing.T) {
	for _, action := range []string{"remove", "replace", "reconnect"} {
		t.Run(action, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, fc := newToolListTestServer(t)
				release := make(chan struct{})
				c := newToolListConnection("svc", func(_ context.Context, n int32) (transport.ToolList, error) {
					if n == 2 {
						<-release
					}
					return toolListNamed("late", transport.FreshUntil(fc.Now().Add(time.Hour))), nil
				})
				u := addToolListConnection(t, s, c)
				u.refresher.request()
				synctest.Wait()
				replacement := toolListNamed("replacement", transport.FreshUntil(fc.Now()))
				changeLifecycle(t, lifecycleChange{Server: s, Upstream: u, Action: action, Replacement: replacement})
				close(release)
				s.refreshWg.Wait()
				names := strings.Join(registeredToolNames(s), ",")
				if strings.Contains(names, "late") || (action != "remove" && !strings.Contains(names, "replacement")) {
					t.Fatalf("tools after a late refresh = %s; want only the replacement", names)
				}
				if action == "reconnect" && !u.refresher.freshness.Stale(fc.Now()) {
					t.Fatal("a late refresh made the reconnected list fresh")
				}
			})
		})
	}
}

type lifecycleChange struct {
	Server      *Server
	Upstream    *upstreamServer
	Action      string
	Replacement transport.ToolList
}

func changeLifecycle(t *testing.T, change lifecycleChange) {
	t.Helper()
	s, replacementConn := change.Server, &transport.FakeConnection{Tools: change.Replacement.Tools}
	switch change.Action {
	case "remove":
		if _, err := s.removeServerFromAgent("svc"); err != nil {
			t.Fatal(err)
		}
	case "replace":
		if err := s.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, replacementConn); err != nil {
			t.Fatal(err)
		}
	case "reconnect":
		if !s.swapConn(change.Upstream, replacementConn, change.Replacement) {
			t.Fatal("reconnect rejected")
		}
	}
}

func TestToolListRefreshKeepsAliasesAndPermissions(t *testing.T) {
	s, fc := newToolListTestServer(t)
	c := newToolListConnection("svc", func(context.Context, int32) (transport.ToolList, error) {
		return transport.ToolList{
			Tools: []transport.ToolDefinition{
				{Name: "visible", InputSchema: json.RawMessage(`{}`)},
				{Name: "secret"},
			},
			Freshness: transport.FreshUntil(fc.Now()),
		}, nil
	})
	sc := config.ServerConfig{
		Name:        "svc",
		Projections: map[string]*config.ProjectionConfig{"visible": {Alias: "alias"}},
		Permissions: &config.PermissionsConfig{Default: config.PermOpen, Hidden: []string{"secret"}},
	}
	if err := s.AddConnection(t.Context(), sc, c); err != nil {
		t.Fatal(err)
	}
	if got := readToolsList(s); !strings.Contains(got, "svc__alias") || strings.Contains(got, "secret") {
		t.Fatalf("refreshed tools/list = %s; want the alias and no hidden tool", got)
	}
	entry, err := s.reg.Lookup("svc.alias")
	if err != nil || entry.Permission != config.PermOpen {
		t.Fatalf("refreshed alias = %+v, %v; want open permission", entry, err)
	}
}
