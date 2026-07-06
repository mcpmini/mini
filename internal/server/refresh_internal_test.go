//go:build test

package server

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestToolRefreshRefreshesOnlyNotifyingUpstreamBeforeNotification(t *testing.T) {
	srv := newRefreshTestServer(t)
	a := newNotificationFake(toolDef("old", `{"type":"object"}`))
	b := newNotificationFake(toolDef("other", `{"type":"object"}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "a"}, a); err != nil {
		t.Fatal(err)
	}
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "b"}, b); err != nil {
		t.Fatal(err)
	}
	waitListCall(t, a)
	waitListCall(t, b)
	session, notifications := proxyNotificationSession(t, srv)
	defer session.disableNotifications(notifications)

	a.replace(toolDef("new", `{"type":"object","required":["value"]}`))
	notification := mustNotification(t, notifications)
	if notification.Method != transport.NotificationToolsChanged {
		t.Fatalf("method = %q", notification.Method)
	}

	names := toolNames(srv.reg.AllFull())
	if !slices.Equal(names, []string{"a.new", "b.other"}) {
		t.Fatalf("names = %v", names)
	}
	select {
	case <-b.listCall:
		t.Fatal("unrelated upstream was refreshed")
	default:
	}
}

func TestToolRefreshSemanticSchemaEqualityDoesNotNotify(t *testing.T) {
	srv := newRefreshTestServer(t)
	conn := newNotificationFake(toolDef("same", `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"integer"}}}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	waitListCall(t, conn)
	session, notifications := proxyNotificationSession(t, srv)
	defer session.disableNotifications(notifications)

	conn.replace(toolDef("same", `{"properties":{"b":{"type":"integer"},"a":{"type":"string"}},"type":"object"}`))
	waitListCall(t, conn)
	waitRefreshIdle(t, srv, "svc")
	assertNoBufferedNotification(t, notifications)
}

func TestToolRefreshPreservesActions(t *testing.T) {
	srv := newRefreshTestServer(t)
	conn := newNotificationFake(toolDef("base", `{"type":"object"}`), toolDef("gone", `{"type":"object"}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	waitListCall(t, conn)
	srv.RegisterAction(config.ActionConfig{Name: "act", Server: "svc", Tool: "base"})

	conn.replace(toolDef("base", `{"type":"object"}`), toolDef("new", `{"type":"object"}`))
	waitListCall(t, conn)
	waitRefreshIdle(t, srv, "svc")

	entry, err := srv.reg.Lookup("svc.act")
	if err != nil {
		t.Fatalf("action disappeared after refresh: %v", err)
	}
	raw, _ := json.Marshal(executeParams{Server: "svc", Tool: "act"})
	if _, err := srv.handleExecute(t.Context(), raw, newSession(srv.clock)); err != nil {
		t.Fatalf("action call failed after refresh: %v", err)
	}
	call := conn.lastToolCall()
	if call.Name != "base" {
		t.Fatalf("upstream tool = %q, want %q", call.Name, "base")
	}
	if entry.TargetTool != "base" {
		t.Fatalf("TargetTool = %q, want %q", entry.TargetTool, "base")
	}
}
