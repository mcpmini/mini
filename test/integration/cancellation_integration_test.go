//go:build integration

package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/config/configtest"
	"github.com/mcpmini/mini/internal/transport"
)

func TestIntegrationLegacyCancellationReachesUpstream(t *testing.T) {
	for _, timeout := range []string{"0", "200ms"} {
		t.Run("tool_timeout="+timeout, func(t *testing.T) { checkLegacyCancellation(t, timeout) })
	}
}

func checkLegacyCancellation(t *testing.T, timeout string) {
	calls, notices := make(chan json.RawMessage, 1), make(chan json.RawMessage, 1)
	srv := cancellationUpstream(t, calls, notices)
	cfg := t.TempDir()
	configtest.WriteServer(
		t,
		cfg,
		config.ServerConfig{Name: "svc", Transport: "sse", URL: srv.URL, ToolTimeout: timeout},
	)
	client := startServer(t, cfg)
	result := make(chan bool, 1)
	go func() { _, isErr := client.execToolAllowError("svc", "get_item", nil); result <- isErr }()
	upstreamID := awaitCancellationID(t, calls)
	if timeout == "0" {
		client.sendNotification(transport.NotificationCancelled, map[string]any{"requestId": client.nextID.Load()})
	}
	if noticeID := awaitCancellationID(t, notices); string(noticeID) != string(upstreamID) {
		t.Fatalf("cancellation targets %s, want upstream request %s", noticeID, upstreamID)
	}
	select {
	case isErr := <-result:
		if !isErr {
			t.Fatal("abandoned tool call succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled call did not finish")
	}
	if _, isErr := client.execToolAllowError("svc", "get_item", nil); isErr {
		t.Fatal("subsequent tool call failed after cancellation")
	}
}

func cancellationUpstream(t *testing.T, calls, notices chan<- json.RawMessage) *httptest.Server {
	t.Helper()
	var callCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Method == "server/discover" {
			_ = json.NewEncoder(w).
				Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "legacy"}})
			return
		}
		if req.Method == transport.NotificationCancelled {
			var params transport.CancelledParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				t.Error(err)
				return
			}
			notices <- params.RequestID
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if req.Method == "tools/call" && callCount.Add(1) == 1 {
			calls <- req.ID
			<-r.Context().Done()
			return
		}
		_ = json.NewEncoder(w).
			Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": fakeMCPResult(req.Method)})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func awaitCancellationID(t *testing.T, ids <-chan json.RawMessage) json.RawMessage {
	t.Helper()
	select {
	case id := <-ids:
		return id
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not receive the expected call or cancellation")
		return nil
	}
}
