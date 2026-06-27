//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

const stressIterations = 500

// TestConcurrentConfigureExec triggers configure+call contention to expose races
// between Server.mu write (configure) and read (call) paths.
func TestConcurrentConfigureExec(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	srv.AddConnection(ctx, config.ServerConfig{Name: "alpha"}, fakeConnWithResponse("toolA", `{"content":[{"type":"text","text":"a"}]}`))
	srv.AddConnection(ctx, config.ServerConfig{Name: "beta"}, fakeConnWithResponse("toolB", `{"content":[{"type":"text","text":"b"}]}`))

	goroutinesBefore := runtime.NumGoroutine()
	var wg sync.WaitGroup

	for range 5 {
		wg.Add(1)
		go runExecWorker(t, srv, &wg)
	}
	for range 2 {
		wg.Add(1)
		go runConfigureWorker(t, srv, ctx, &wg)
	}
	wg.Wait()

	if delta := runtime.NumGoroutine() - goroutinesBefore; delta > 5 {
		t.Errorf("goroutine leak: started with %d, ended with %d (delta %d)", goroutinesBefore, goroutinesBefore+delta, delta)
	}
}

func runExecWorker(t *testing.T, srv *server.Server, wg *sync.WaitGroup) {
	t.Helper()
	defer wg.Done()
	for range stressIterations {
		resp := serve(t, srv, callTool("call", map[string]any{
			"server": "alpha", "tool": "toolA", "params": map[string]any{},
		}))
		_ = resp["error"]
	}
}

func runConfigureWorker(t *testing.T, srv *server.Server, ctx context.Context, wg *sync.WaitGroup) {
	t.Helper()
	defer wg.Done()
	fake := fakeConnWithResponse("toolC", `{"content":[{"type":"text","text":"c"}]}`)
	for range stressIterations {
		srv.AddConnection(ctx, config.ServerConfig{Name: "dynamic"}, fake)
		serve(t, srv, callTool("config", map[string]any{"action": "remove_server", "server": "dynamic"}))    //nolint:errcheck
		serve(t, srv, callTool("config", map[string]any{                                                      //nolint:errcheck
			"action": "set_projection", "server": "alpha", "tool": "toolA",
			"projection": map[string]any{"string_limit": 50}, "session_only": true,
		}))
	}
}

func fakeConnWithResponse(toolName, response string) *transport.FakeConnection {
	f := &transport.FakeConnection{
		Tools: []transport.ToolDefinition{
			{Name: toolName, Description: toolName, InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		Responses: map[string]json.RawMessage{
			"tools/call": json.RawMessage(response),
		},
	}
	return f
}
