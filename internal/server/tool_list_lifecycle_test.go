//go:build test

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestToolListReadersShareOneRefreshPerUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, fc := newToolListTestServer(t)
		release := make(chan struct{})
		connections := make([]*toolListConnection, 0, 2)
		for _, name := range []string{"one", "two"} {
			c := newToolListConnection(func(ctx context.Context, n int32) (transport.ToolList, error) {
				if n == 1 {
					return toolListNamed("old", transport.FreshUntil(fc.Now().Add(time.Second))), nil
				}
				select {
				case <-release:
				case <-ctx.Done():
					return transport.ToolList{}, ctx.Err()
				}
				return toolListNamed("new", transport.FreshUntil(fc.Now().Add(time.Second))), nil
			})
			if err := s.AddConnection(t.Context(), config.ServerConfig{Name: name}, c); err != nil {
				t.Fatal(err)
			}
			connections = append(connections, c)
		}
		fc.Advance(time.Second)
		var wg sync.WaitGroup
		results := make([]string, 10)
		for i := range results {
			wg.Go(func() {
				result, err := s.handleToolsList(context.Background(), newSession(fc))
				raw, _ := json.Marshal(result)
				results[i] = fmt.Sprint(string(raw), err)
			})
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

func TestToolListLateRefreshCannotPublishAfterLifecycleChange(t *testing.T) {
	for _, action := range []string{"remove", "replace", "reconnect"} {
		t.Run(action, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, fc := newToolListTestServer(t)
				release := make(chan struct{})
				c := newToolListConnection(func(_ context.Context, n int32) (transport.ToolList, error) {
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

func TestToolListShutdownReleasesWaitingReaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, fc := newToolListTestServer(t)
		c := newToolListConnection(func(ctx context.Context, n int32) (transport.ToolList, error) {
			if n == 2 {
				<-ctx.Done()
				return transport.ToolList{}, ctx.Err()
			}
			return toolListNamed("old", transport.FreshUntil(fc.Now())), nil
		})
		u := addToolListConnection(t, s, c)
		read := make(chan struct{})
		go func() { defer close(read); _, _ = s.handleToolsList(t.Context(), newSession(fc)) }()
		synctest.Wait()
		u.shutdown()
		<-read
		s.refreshWg.Wait()
	})
}

func TestToolListShutdownReleasesDiscoveryReadsWithTheListTheyHave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, fc := newToolListTestServer(t)
		c := newToolListConnection(func(ctx context.Context, n int32) (transport.ToolList, error) {
			if n == 2 {
				<-ctx.Done()
				return transport.ToolList{}, ctx.Err()
			}
			return toolListNamed("old", transport.FreshUntil(fc.Now())), nil
		})
		addToolListConnection(t, s, c)
		read := make(chan string, 1)
		go func() {
			result, err := s.handleToolsList(context.Background(), newSession(fc))
			raw, _ := json.Marshal(result)
			read <- fmt.Sprint(string(raw), err)
		}()
		synctest.Wait()
		started := time.Now()
		s.ReleaseStartupHolds()
		got := <-read
		if elapsed := time.Since(started); elapsed != 0 || !strings.Contains(got, "svc__old") {
			t.Fatalf("read during shutdown = %s after %v; want the list it had at once", got, elapsed)
		}
		s.refreshWg.Wait()
	})
}

func TestToolListRefreshKeepsAliasesAndPermissions(t *testing.T) {
	s, fc := newToolListTestServer(t)
	c := newToolListConnection(func(context.Context, int32) (transport.ToolList, error) {
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
	if got := proxyToolsList(t, s); !strings.Contains(got, "svc__alias") || strings.Contains(got, "secret") {
		t.Fatalf("refreshed tools/list = %s; want the alias and no hidden tool", got)
	}
	result, err := s.handleList(t.Context(), json.RawMessage(`{"tool":"svc.alias","detail":true}`))
	raw, _ := json.Marshal(result)
	if err != nil || !strings.Contains(string(raw), `"permission":"open"`) {
		t.Fatalf("refreshed detail = %s, %v; want its permission", raw, err)
	}
}

func TestToolListHungUpstreamsCostEachReadOneBoundedWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, fc := newToolListTestServer(t)
		for _, name := range []string{"one", "two"} {
			c := newToolListConnection(func(ctx context.Context, n int32) (transport.ToolList, error) {
				if n == 2 {
					<-ctx.Done()
					return transport.ToolList{}, ctx.Err()
				}
				return toolListNamed(oldThenNew(n), transport.FreshUntil(fc.Now().Add(time.Second))), nil
			})
			if err := s.AddConnection(t.Context(), config.ServerConfig{Name: name}, c); err != nil {
				t.Fatal(err)
			}
		}
		fc.Advance(time.Second)
		for read := 1; read <= 2; read++ {
			started := time.Now()
			got := proxyToolsList(t, s)
			if elapsed := time.Since(started); elapsed != discoveryWait {
				t.Fatalf("read %d waited %v, want one request-wide %v", read, elapsed, discoveryWait)
			}
			if !strings.Contains(got, "one__old") || !strings.Contains(got, "two__old") {
				t.Fatalf("read %d during the hang = %s; want both lists it had", read, got)
			}
		}
		s.refreshWg.Wait()
		if got := proxyToolsList(t, s); !strings.Contains(got, "one__new") || !strings.Contains(got, "two__new") {
			t.Fatalf("next read = %s; want both lists refetched", got)
		}
	})
}

func TestToolListSlowRefreshPublishesAfterTheReadGivesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, fc := newToolListTestServer(t)
		slowAnswer := discoveryWait + 15*time.Second
		c := newToolListConnection(func(_ context.Context, n int32) (transport.ToolList, error) {
			if n == 1 {
				return toolListNamed("old", transport.FreshUntil(fc.Now())), nil
			}
			time.Sleep(slowAnswer)
			return toolListNamed("new", transport.FreshUntil(fc.Now().Add(time.Hour))), nil
		})
		addToolListConnection(t, s, c)
		if got := proxyToolsList(t, s); !strings.Contains(got, "svc__old") {
			t.Fatalf("read that gave up = %s; want the list it had", got)
		}
		s.refreshWg.Wait()
		if got := proxyToolsList(t, s); !strings.Contains(got, "svc__new") || c.calls.Load() != 2 {
			t.Fatalf("read after the slow refresh = %s after %d fetches; want its list", got, c.calls.Load())
		}
	})
}

func TestToolListDiscoveryWaitIncludesTheStartupHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newInternalTestServer(t)
		c := newToolListConnection(func(ctx context.Context, n int32) (transport.ToolList, error) {
			if n == 2 {
				<-ctx.Done()
				return transport.ToolList{}, ctx.Err()
			}
			return toolListNamed("old", transport.FreshUntil(s.clock.Now())), nil
		})
		addToolListConnection(t, s, c)
		s.recordConfigServers([]config.ServerConfig{{Name: "connecting"}})
		s.openConnectWindow("connecting")

		started := time.Now()
		got := proxyToolsList(t, s)
		if elapsed := time.Since(started); elapsed != discoveryWait {
			t.Fatalf("a read held for startup then a hung refresh waited %v, want %v in all", elapsed, discoveryWait)
		}
		if !strings.Contains(got, "svc__old") {
			t.Fatalf("read = %s; want the list it had", got)
		}
	})
}
