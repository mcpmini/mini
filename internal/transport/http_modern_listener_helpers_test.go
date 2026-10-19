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

const modernToolsChangedMessage = `{"jsonrpc":"2.0",` +
	`"method":"notifications/tools/list_changed",` +
	`"params":{"_meta":{"io.modelcontextprotocol/subscriptionId":2}}}`

const modernAckWithoutTools = `{"jsonrpc":"2.0","method":"notifications/subscriptions/acknowledged",` +
	`"params":{"notifications":{}}}`

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
		if req.Method != modernListenMethod {
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
		r.Header.Get("Mcp-Method") != modernListenMethod ||
		!strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		t.Errorf("modern listen headers = %v", r.Header)
	}
}

func writeAck(t *testing.T, w http.ResponseWriter, id any) {
	t.Helper()
	ack := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			Meta struct {
				SubscriptionID any `json:"io.modelcontextprotocol/subscriptionId"`
			} `json:"_meta"`
			Notifications listenNotifications `json:"notifications"`
		} `json:"params"`
	}{JSONRPC: "2.0", Method: "notifications/subscriptions/acknowledged"}
	ack.Params.Meta.SubscriptionID = id
	ack.Params.Notifications.ToolsListChanged = true
	message, err := json.Marshal(ack)
	if err != nil {
		t.Errorf("encode acknowledgement: %v", err)
		return
	}
	writeSSEMessage(t, w, string(message))
	w.(http.Flusher).Flush()
}

func writeSSEMessage(t *testing.T, w http.ResponseWriter, message string) {
	t.Helper()
	if _, err := fmt.Fprintf(w, "data: %s\n\n", message); err != nil {
		t.Errorf("write notification: %v", err)
	}
}

func writeComplete(t *testing.T, w http.ResponseWriter, id any) {
	t.Helper()
	var result struct {
		ResultType string `json:"resultType"`
		Meta       struct {
			SubscriptionID any `json:"io.modelcontextprotocol/subscriptionId"`
		} `json:"_meta"`
	}
	result.ResultType = "complete"
	result.Meta.SubscriptionID = id
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Errorf("encode completion result: %v", err)
		return
	}
	message, err := json.Marshal(Response{JSONRPC: "2.0", ID: id, Result: resultJSON})
	if err != nil {
		t.Errorf("encode completion response: %v", err)
		return
	}
	writeSSEMessage(t, w, string(message))
	w.(http.Flusher).Flush()
}
