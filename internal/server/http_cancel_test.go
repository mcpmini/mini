//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

// TestHTTPCancellation_CancelsInFlightCall proves notifications/cancelled aborts
// the in-flight upstream call over the HTTP daemon path, identical to stdio.
// All POSTs for one session share a *Session, so registration in servePost lets
// a later cancel notification reach the running request's context.
func TestHTTPCancellation_CancelsInFlightCall(t *testing.T) {
	srv, ts := newHTTPTestServer(t)

	var callStarted sync.WaitGroup
	callStarted.Add(1)
	var callCtx context.Context

	blocking := &blockingFakeConn{
		tools: []transport.ToolDefinition{{
			Name:        "slow",
			Description: "slow tool",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		onCall: func(ctx context.Context) {
			callCtx = ctx
			callStarted.Done()
			<-ctx.Done()
		},
	}
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "s"}, blocking)

	sessionID := initSession(t, ts)
	go drainMCPPost(t, ts, callToolWithID(42, "call", map[string]any{
		"server": "s",
		"tool":   "slow",
		"params": map[string]any{},
	}), sessionID)

	if !waitTimeout(&callStarted, 3*time.Second) {
		t.Fatal("upstream call did not start within timeout")
	}

	drainMCPPost(t, ts, notification("notifications/cancelled", map[string]any{"requestId": 42}), sessionID)

	select {
	case <-callCtx.Done():
	case <-time.After(2 * time.Second):
		t.Error("upstream call context was not cancelled after notifications/cancelled over HTTP")
	}
}
