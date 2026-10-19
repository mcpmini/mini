//go:build test

package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func modernListenerServer(
	t *testing.T,
	advertise bool,
	listen func(http.ResponseWriter, *http.Request, Request),
) *httptest.Server {
	t.Helper()
	return newModernServer(t, modernUpstream{ListChanged: advertise, Handle: listenOnly(t, listen)})
}

// listenOnly hands subscriptions/listen requests to listen as an SSE stream and answers the rest.
func listenOnly(
	t *testing.T,
	listen func(http.ResponseWriter, *http.Request, Request),
) func(http.ResponseWriter, *http.Request, Request) {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request, req Request) {
		if req.Method != MethodSubscriptionsListen {
			writeRPCResult(t, w, req.ID, map[string]bool{"ok": true})
			return
		}
		assertModernListenHeaders(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		listen(w, r, req)
	}
}

func assertModernListenHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Header.Get("MCP-Protocol-Version") != ModernVersions[0] ||
		r.Header.Get("Mcp-Method") != MethodSubscriptionsListen ||
		!strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		t.Errorf("modern listen headers = %v", r.Header)
	}
}

func writeAck(t *testing.T, w http.ResponseWriter, id any) {
	t.Helper()
	writeSSEJSON(t, w, NewSubscriptionAck(rawID(t, id), ListenNotifications{ToolsListChanged: true}))
}

func writeSSEJSON(t *testing.T, w http.ResponseWriter, message any) {
	t.Helper()
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Errorf("encode SSE message: %v", err)
		return
	}
	writeSSEMessage(t, w, string(encoded))
	w.(http.Flusher).Flush()
}

func rawID(t *testing.T, id any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("encode request id: %v", err)
	}
	return encoded
}

func writeSSEMessage(t *testing.T, w http.ResponseWriter, message string) {
	t.Helper()
	if _, err := fmt.Fprintf(w, "data: %s\n\n", message); err != nil {
		t.Errorf("write notification: %v", err)
	}
}

type listenCompleteResult struct {
	ResultType ResultType       `json:"resultType"`
	Meta       SubscriptionMeta `json:"_meta"`
}

func writeComplete(t *testing.T, w http.ResponseWriter, id any) {
	t.Helper()
	result, err := json.Marshal(
		listenCompleteResult{ResultType: ResultComplete, Meta: SubscriptionMeta{SubscriptionID: rawID(t, id)}},
	)
	if err != nil {
		t.Errorf("encode completion result: %v", err)
		return
	}
	writeSSEJSON(t, w, Response{JsonRPC: JsonRPCVersion, ID: id, Result: result})
}
