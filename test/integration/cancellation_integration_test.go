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
	up := newCancellationUpstream(t)
	client := startServer(t, legacyHTTPServerConfig(t, up.URL, timeout))
	callFailed := make(chan bool, 1)
	go func() { _, isErr := client.execToolAllowError("svc", "get_item", nil); callFailed <- isErr }()
	upstreamID := receiveWithin(t, up.heldCallIDs, "the held tool call")
	if timeout == "0" {
		client.sendNotification(transport.NotificationCancelled, map[string]any{"requestId": client.nextID.Load()})
	}
	if noticeID := receiveWithin(t, up.noticeIDs, "a cancellation"); string(noticeID) != string(upstreamID) {
		t.Fatalf("cancellation targets %s, want upstream request %s", noticeID, upstreamID)
	}
	if !receiveWithin(t, callFailed, "the cancelled call's result") {
		t.Fatal("abandoned tool call succeeded")
	}
	if _, isErr := client.execToolAllowError("svc", "get_item", nil); isErr {
		t.Fatal("subsequent tool call failed after cancellation")
	}
}

func legacyHTTPServerConfig(t *testing.T, url, toolTimeout string) string {
	cfg := t.TempDir()
	server := config.ServerConfig{Name: "svc", Transport: "sse", URL: url, ToolTimeout: toolTimeout}
	configtest.WriteServer(t, cfg, server)
	return cfg
}

type cancellationUpstream struct {
	URL         string
	heldCallIDs chan json.RawMessage
	noticeIDs   chan json.RawMessage
	toolCalls   atomic.Int64
}

func newCancellationUpstream(t *testing.T) *cancellationUpstream {
	up := &cancellationUpstream{heldCallIDs: make(chan json.RawMessage, 1), noticeIDs: make(chan json.RawMessage, 1)}
	srv := httptest.NewServer(http.HandlerFunc(up.serveHTTP))
	t.Cleanup(srv.Close)
	up.URL = srv.URL
	return up
}

func (up *cancellationUpstream) serveHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	switch {
	case req.Method == "server/discover":
		writeRPC(w, map[string]any{"id": req.ID, "error": map[string]any{"code": -32601, "message": "legacy"}})
	case req.Method == transport.NotificationCancelled:
		up.recordNotice(w, req.Params)
	case req.Method == "tools/call" && up.toolCalls.Add(1) == 1:
		up.heldCallIDs <- req.ID
		<-r.Context().Done()
	default:
		writeRPC(w, map[string]any{"id": req.ID, "result": fakeMCPResult(req.Method)})
	}
}

func (up *cancellationUpstream) recordNotice(w http.ResponseWriter, raw json.RawMessage) {
	var params transport.CancelledParams
	_ = json.Unmarshal(raw, &params) // Malformed params leave RequestID empty, which fails the ID comparison.
	up.noticeIDs <- params.RequestID
	w.WriteHeader(http.StatusAccepted)
}

func writeRPC(w http.ResponseWriter, message map[string]any) {
	message["jsonrpc"] = transport.JsonRPCVersion
	_ = json.NewEncoder(w).Encode(message) // A failed write surfaces as the client's request error.
}

func receiveWithin[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}
