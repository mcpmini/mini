//go:build test

package server

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestToolRefreshRetryExhaustionPreservesCatalog(t *testing.T) {
	fakeClock := clock.NewFake()
	srv := newRefreshTestServer(t, WithClock(fakeClock))
	conn := newNotificationFake(toolDef("old", `{"type":"object"}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	waitListCall(t, conn)
	session, notifications := proxyNotificationSession(t, srv)
	defer session.disableNotifications(notifications)

	conn.queueListSteps(
		listStep{err: errors.New("boom 1")},
		listStep{err: errors.New("boom 2")},
		listStep{err: errors.New("boom 3")},
	)
	conn.notifyToolsChanged()
	waitListCall(t, conn)
	waitFakeTimer(t, fakeClock, time.Second)
	fakeClock.Advance(time.Second)
	waitListCall(t, conn)
	waitFakeTimer(t, fakeClock, 2*time.Second)
	fakeClock.Advance(2 * time.Second)
	waitListCall(t, conn)
	waitRefreshIdle(t, srv, "svc")

	names := toolNames(srv.reg.AllFull())
	if !slices.Equal(names, []string{"svc.old"}) {
		t.Fatalf("names = %v", names)
	}
	assertNoBufferedNotification(t, notifications)
}

func TestToolRefreshBurstCoalescesToOneExtraRefresh(t *testing.T) {
	srv := newRefreshTestServer(t)
	conn := newNotificationFake(toolDef("old", `{"type":"object"}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	waitListCall(t, conn)
	session, notifications := proxyNotificationSession(t, srv)
	defer session.disableNotifications(notifications)

	started := make(chan struct{})
	release := make(chan struct{})
	conn.queueListSteps(
		listStep{tools: []transport.ToolDefinition{toolDef("new", `{"type":"object"}`)}, started: started, release: release},
		listStep{tools: []transport.ToolDefinition{toolDef("new", `{"type":"object"}`)}},
	)
	conn.notifyToolsChanged()
	waitListCall(t, conn)
	waitClosed(t, started, "first refresh")
	conn.notifyToolsChanged()
	conn.notifyToolsChanged()
	close(release)
	waitListCall(t, conn)
	waitRefreshIdle(t, srv, "svc")

	notification := mustNotification(t, notifications)
	if notification.Method != transport.NotificationToolsChanged {
		t.Fatalf("method = %q", notification.Method)
	}
	assertNoBufferedNotification(t, notifications)
	select {
	case <-conn.listCall:
		t.Fatal("burst scheduled more than one extra refresh")
	default:
	}
}

func TestToolRefreshRemoveRaceDoesNotResurrectServer(t *testing.T) {
	srv := newRefreshTestServer(t)
	conn := newNotificationFake(toolDef("old", `{"type":"object"}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	waitListCall(t, conn)

	started := make(chan struct{})
	release := make(chan struct{})
	conn.queueListSteps(listStep{
		tools:   []transport.ToolDefinition{toolDef("new", `{"type":"object"}`)},
		started: started,
		release: release,
	})
	conn.notifyToolsChanged()
	waitListCall(t, conn)
	waitClosed(t, started, "refresh start")
	srv.detachAndCloseServer("svc")
	close(release)
	waitRefreshIdle(t, srv, "svc")

	if names := toolNames(srv.reg.AllFull()); len(names) != 0 {
		t.Fatalf("names = %v, want empty", names)
	}
}

func TestToolRefreshReplacementRaceKeepsNewCatalog(t *testing.T) {
	srv := newRefreshTestServer(t)
	oldConn := newNotificationFake(toolDef("old", `{"type":"object"}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, oldConn); err != nil {
		t.Fatal(err)
	}
	waitListCall(t, oldConn)

	started := make(chan struct{})
	release := make(chan struct{})
	oldConn.queueListSteps(listStep{
		tools:   []transport.ToolDefinition{toolDef("stale", `{"type":"object"}`)},
		started: started,
		release: release,
	})
	oldConn.notifyToolsChanged()
	waitListCall(t, oldConn)
	waitClosed(t, started, "stale refresh start")

	newConn := newNotificationFake(toolDef("fresh", `{"type":"object"}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, newConn); err != nil {
		t.Fatal(err)
	}
	waitListCall(t, newConn)
	close(release)
	waitRefreshIdle(t, srv, "svc")

	names := toolNames(srv.reg.AllFull())
	if !slices.Equal(names, []string{"svc.fresh"}) {
		t.Fatalf("names = %v", names)
	}
}

func TestToolRefreshShutdownCancelsInFlightRefresh(t *testing.T) {
	srv := newRefreshTestServer(t)
	conn := newNotificationFake(toolDef("old", `{"type":"object"}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	waitListCall(t, conn)

	started := make(chan struct{})
	conn.queueListSteps(listStep{started: started, waitCancel: true})
	conn.notifyToolsChanged()
	waitListCall(t, conn)
	waitClosed(t, started, "refresh start")

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Close()
	}()
	waitClosed(t, done, "server close")
}

func TestToolRefreshShutdownCloseDoesNotDeadlockWithCallback(t *testing.T) {
	srv := newRefreshTestServer(t)
	conn := newCloseWaitingNotificationFake(toolDef("old", `{"type":"object"}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	conn.triggerAfterClose()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Close()
	}()

	waitClosed(t, conn.callbackStarted, "callback start")
	waitClosed(t, done, "server close")
}
