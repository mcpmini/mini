//go:build test

package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// FakeConnection is a test double for Connection.
// Responses, Tools, and Err are set at construction time and treated as
// read-only during concurrent calls. LastParams and Closed are guarded by mu.
type FakeConnection struct {
	Tools     []ToolDefinition
	Responses map[string]json.RawMessage // keyed by method
	Err       error

	mu                  sync.Mutex
	LastParams          json.RawMessage // params from the most recent Call invocation
	Closed              bool
	notificationHandler func(Notification)
}

func (f *FakeConnection) Call(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	f.LastParams = params
	f.mu.Unlock()

	if f.Err != nil {
		return nil, f.Err
	}
	if r, ok := f.Responses[method]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("no fake response for %s", method)
}

func (f *FakeConnection) ListTools(_ context.Context) ([]ToolDefinition, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Tools, nil
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
