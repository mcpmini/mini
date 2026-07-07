//go:build test

package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// FakeConnection is a test double for Connection.
type FakeConnection struct {
	Tools     []ToolDefinition
	Responses map[string]json.RawMessage // keyed by method
	Err       error

	mu                  sync.Mutex
	LastParams          json.RawMessage // params from the most recent Call invocation
	Closed              bool
	ListToolsCalls      int
	notificationHandler func(Notification)
}

func (f *FakeConnection) Call(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	f.LastParams = params
	err := f.Err
	resp, ok := f.Responses[method]
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if ok {
		return resp, nil
	}
	return nil, fmt.Errorf("no fake response for %s", method)
}

func (f *FakeConnection) ListTools(_ context.Context) ([]ToolDefinition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ListToolsCalls++
	if f.Err != nil {
		return nil, f.Err
	}
	return append([]ToolDefinition(nil), f.Tools...), nil
}

func (f *FakeConnection) Health(_ context.Context) error { return f.Err }

func (f *FakeConnection) Close() error {
	f.mu.Lock()
	f.Closed = true
	f.mu.Unlock()
	return nil
}

func (f *FakeConnection) SetNotificationHandler(handler func(Notification)) {
	f.mu.Lock()
	f.notificationHandler = handler
	f.mu.Unlock()
}

func (f *FakeConnection) EmitNotification(notification Notification) {
	f.mu.Lock()
	handler := f.notificationHandler
	f.mu.Unlock()
	if handler != nil {
		handler(notification)
	}
}

func (f *FakeConnection) SetTools(tools []ToolDefinition) {
	f.mu.Lock()
	f.Tools = append([]ToolDefinition(nil), tools...)
	f.mu.Unlock()
}

func (f *FakeConnection) SetError(err error) {
	f.mu.Lock()
	f.Err = err
	f.mu.Unlock()
}

func (f *FakeConnection) ListToolsCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ListToolsCalls
}
