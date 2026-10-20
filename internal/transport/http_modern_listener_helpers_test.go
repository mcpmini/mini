//go:build test

package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func modernListenerServer(
	t *testing.T,
	advertise bool,
	listen upstreamHandler,
) *httptest.Server {
	t.Helper()
	return newModernServer(t, modernUpstream{ListChanged: advertise, Handle: listenOnly(t, listen)})
}

// listenOnly hands subscriptions/listen requests to listen as an SSE stream and answers the rest.
func listenOnly(
	t *testing.T,
	listen upstreamHandler,
) upstreamHandler {
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
		t.Errorf("encode request id: %v", err)
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

func resetOnFirstPing(t *testing.T, next upstreamHandler) upstreamHandler {
	var pings atomic.Int32
	return func(w http.ResponseWriter, r *http.Request, req Request) {
		if req.Method == "ping" && pings.Add(1) == 1 {
			writeHTTPRPCError(t, w, httpRPCErrorParams{id: req.ID, code: CodeUnsupportedProtocolVersion})
			return
		}
		next(w, r, req)
	}
}

func refuseFirstListen(t *testing.T, relisten chan<- struct{}) upstreamHandler {
	var listens atomic.Int32
	return listenOnly(t, func(w http.ResponseWriter, r *http.Request, req Request) {
		if listens.Add(1) == 1 {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeAck(t, w, req.ID)
		relisten <- struct{}{}
		<-r.Context().Done()
	})
}
