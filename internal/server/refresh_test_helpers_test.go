//go:build test

package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/registry"
	"github.com/mcpmini/mini/internal/transport"
)

type listStep struct {
	tools      []transport.ToolDefinition
	err        error
	started    chan struct{}
	release    chan struct{}
	waitCancel bool
}

type notificationFake struct {
	mu         sync.Mutex
	tools      []transport.ToolDefinition
	steps      []listStep
	handler    func(transport.Notification)
	listCall   chan struct{}
	callResult json.RawMessage
	lastCall   transport.ToolCallParams
	closed     bool
}

type closeWaitingNotificationFake struct {
	mu              sync.Mutex
	tools           []transport.ToolDefinition
	handler         func(transport.Notification)
	closeStarted    chan struct{}
	callbackStarted chan struct{}
	callbackDone    chan struct{}
}

func newNotificationFake(tools ...transport.ToolDefinition) *notificationFake {
	return &notificationFake{
		tools:      cloneDefs(tools),
		listCall:   make(chan struct{}, 32),
		callResult: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
	}
}

func newCloseWaitingNotificationFake(tools ...transport.ToolDefinition) *closeWaitingNotificationFake {
	return &closeWaitingNotificationFake{
		tools:           cloneDefs(tools),
		closeStarted:    make(chan struct{}),
		callbackStarted: make(chan struct{}),
		callbackDone:    make(chan struct{}),
	}
}

func cloneDefs(tools []transport.ToolDefinition) []transport.ToolDefinition {
	return append([]transport.ToolDefinition(nil), tools...)
}

func (f *notificationFake) Call(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if method != "tools/call" {
		return json.RawMessage(`null`), nil
	}
	var call transport.ToolCallParams
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.lastCall = call
	result := bytesClone(f.callResult)
	f.mu.Unlock()
	return result, nil
}

func bytesClone(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func (f *notificationFake) ListTools(ctx context.Context) ([]transport.ToolDefinition, error) {
	select {
	case f.listCall <- struct{}{}:
	default:
	}
	step, tools := f.nextListStep()
	if step.started != nil {
		close(step.started)
	}
	if step.release != nil {
		select {
		case <-step.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if step.waitCancel {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if step.err != nil {
		return nil, step.err
	}
	if step.tools != nil {
		tools = cloneDefs(step.tools)
	}
	return tools, nil
}

func (f *notificationFake) nextListStep() (listStep, []transport.ToolDefinition) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tools := cloneDefs(f.tools)
	if len(f.steps) == 0 {
		return listStep{}, tools
	}
	step := f.steps[0]
	f.steps = f.steps[1:]
	return step, tools
}

func (f *notificationFake) queueListSteps(steps ...listStep) {
	f.mu.Lock()
	f.steps = append(f.steps, steps...)
	f.mu.Unlock()
}

func (f *notificationFake) Health(context.Context) error { return nil }

func (f *notificationFake) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *notificationFake) SetNotificationHandler(handler func(transport.Notification)) {
	f.mu.Lock()
	f.handler = handler
	f.mu.Unlock()
}

func (f *notificationFake) replace(tools ...transport.ToolDefinition) {
	f.mu.Lock()
	f.tools = cloneDefs(tools)
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		handler(transport.Notification{JSONRPC: "2.0", Method: transport.NotificationToolsChanged})
	}
}

func (f *notificationFake) notifyToolsChanged() {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		handler(transport.Notification{JSONRPC: "2.0", Method: transport.NotificationToolsChanged})
	}
}

func (f *notificationFake) lastToolCall() transport.ToolCallParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastCall
}

func (f *closeWaitingNotificationFake) Call(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`null`), nil
}

func (f *closeWaitingNotificationFake) ListTools(context.Context) ([]transport.ToolDefinition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneDefs(f.tools), nil
}

func (f *closeWaitingNotificationFake) Health(context.Context) error { return nil }

func (f *closeWaitingNotificationFake) Close() error {
	close(f.closeStarted)
	<-f.callbackDone
	return nil
}

func (f *closeWaitingNotificationFake) SetNotificationHandler(handler func(transport.Notification)) {
	f.mu.Lock()
	f.handler = handler
	f.mu.Unlock()
}

func (f *closeWaitingNotificationFake) triggerAfterClose() {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	go func() {
		<-f.closeStarted
		close(f.callbackStarted)
		handler(transport.Notification{JSONRPC: "2.0", Method: transport.NotificationToolsChanged})
		close(f.callbackDone)
	}()
}

func newRefreshTestServer(t *testing.T, opts ...ServerOption) *Server {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	srv := NewWithConfigDir(cfg, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)), opts...)
	t.Cleanup(srv.Close)
	return srv
}

func proxyNotificationSession(t *testing.T, srv *Server) (*Session, chan json.RawMessage) {
	t.Helper()
	session := srv.sessions.getOrCreate(transport.NewSessionID())
	session.setToolModeOnce(transport.ToolModeProxy)
	session.markInitialized()
	session.markClientReady()
	stream, ok := session.enableNotifications()
	if !ok {
		t.Fatal("enableNotifications() = closed")
	}
	return session, stream
}

func waitListCall(t *testing.T, conn *notificationFake) {
	t.Helper()
	select {
	case <-conn.listCall:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tools/list")
	}
}

func waitRefreshIdle(t *testing.T, srv *Server, serverName string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		srv.mu.RLock()
		u := srv.upstreams[serverName]
		srv.mu.RUnlock()
		if u == nil {
			return
		}
		u.refreshMu.Lock()
		idle := !u.refreshing && !u.refreshAgain
		u.refreshMu.Unlock()
		if idle {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("refresh for %s did not go idle", serverName)
}

func waitClosed(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func mustNotification(t *testing.T, ch <-chan json.RawMessage) transport.Notification {
	t.Helper()
	select {
	case raw := <-ch:
		var notification transport.Notification
		if err := json.Unmarshal(raw, &notification); err != nil {
			t.Fatalf("unmarshal notification: %v", err)
		}
		return notification
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for downstream notification")
		return transport.Notification{}
	}
}

func assertNoBufferedNotification(t *testing.T, ch <-chan json.RawMessage) {
	t.Helper()
	select {
	case raw := <-ch:
		t.Fatalf("unexpected notification: %s", raw)
	default:
	}
}

func toolNames(entries []*registry.ToolEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.FullName)
	}
	slices.Sort(names)
	return names
}

func waitFakeTimer(t *testing.T, fakeClock *clock.Fake, delay time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fakeClock.BlockUntilTimerContext(ctx, delay); err != nil {
		t.Fatalf("BlockUntilTimerContext: %v", err)
	}
}

func toolDef(name, schema string) transport.ToolDefinition {
	return transport.ToolDefinition{Name: name, Description: name, InputSchema: json.RawMessage(schema)}
}
