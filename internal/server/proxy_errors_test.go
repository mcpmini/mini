//go:build test

package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/proxy"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/transport/transporttest"
)

const requiredFormCapability = `{"requiredCapabilities":{"elicitation":{"form":{}}}}`

func missingFormCapability() *transport.RPCError {
	return &transport.RPCError{
		Code:    transport.CodeMissingRequiredClientCapability,
		Message: "Form confirmation requires elicitation",
		Data:    json.RawMessage(requiredFormCapability),
	}
}

func TestModernProxyPreservesMissingCapabilityErrors(t *testing.T) {
	tests := []struct {
		name string
		id   json.RawMessage
		err  error
	}{
		{"numeric ID", json.RawMessage(`41`), missingFormCapability()},
		{
			"wrapped error and string ID",
			json.RawMessage(`"agent-request"`),
			fmt.Errorf("upstream call: %w", missingFormCapability()),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, conn := proxyErrorServer(t, tc.err)
			request := transporttest.ModernRequest(
				tc.id,
				"tools/call",
				transport.ToolCallParams{Name: "svc__confirm", Arguments: map[string]any{}},
			)
			messages := serveModern(t, srv, request)
			requireMissingCapabilityResponse(t, messages, tc.id)
			if got := conn.toolCalls.Load(); got != 1 {
				t.Fatalf("upstream tool calls = %d, want 1", got)
			}
		})
	}
}

func proxyErrorServer(t *testing.T, upstreamErr error) (*server.Server, *countingConn) {
	t.Helper()
	srv := newTestServer(t, server.Params{})
	conn := &countingConn{FakeConnection: fakeConn("confirm")}
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	conn.SetError(upstreamErr)
	return srv, conn
}

func requireMissingCapabilityResponse(t *testing.T, messages []modernMessage, id json.RawMessage) {
	t.Helper()
	if len(messages) != 1 {
		t.Fatalf("messages = %+v, want one response", messages)
	}
	response := messages[0]
	if string(response.ID) != string(id) || response.Error == nil || len(response.Result) != 0 {
		t.Fatalf("response = %+v, want JSON-RPC error for ID %s without a result", response, id)
	}
	expected := missingFormCapability()
	if response.Error.Code != expected.Code || response.Error.Message != expected.Message ||
		string(response.Error.Data) != requiredFormCapability {
		t.Fatalf(
			"error = %+v, want code %d, message %q and data %s",
			response.Error,
			expected.Code,
			expected.Message,
			requiredFormCapability,
		)
	}
}

func TestModernProxyPreservesMissingCapabilityThroughDaemonBridge(t *testing.T) {
	srv, conn := proxyErrorServer(t, missingFormCapability())
	id := json.RawMessage(`"agent-request"`)
	request := transporttest.ModernRequest(
		id,
		"tools/call",
		transport.ToolCallParams{Name: "svc__confirm", Arguments: map[string]any{}},
	)
	message := serveThroughDaemonBridge(t, srv, request)
	requireMissingCapabilityResponse(t, []modernMessage{message}, id)
	if got := conn.toolCalls.Load(); got != 1 {
		t.Fatalf("upstream tool calls through bridge = %d, want 1 without replay", got)
	}
}

func serveThroughDaemonBridge(t *testing.T, srv *server.Server, request []byte) modernMessage {
	t.Helper()
	endpoint := httptest.NewServer(srv)
	t.Cleanup(endpoint.Close)
	client := endpoint.Client()
	client.Transport = daemonBridgeTransport{base: client.Transport, address: endpoint.Listener.Addr().String()}
	var output bytes.Buffer
	if err := proxy.Run(proxy.RunParams{
		Client: client, SessionID: "12345678-1234-1234-1234-123456789012",
		In: bytes.NewReader(request), Out: &output, Clock: clock.NewFake(),
	}); err != nil {
		t.Fatal(err)
	}
	var message modernMessage
	if err := json.Unmarshal(output.Bytes(), &message); err != nil {
		t.Fatalf("decode bridge response %q: %v", output.String(), err)
	}
	return message
}

type daemonBridgeTransport struct {
	base    http.RoundTripper
	address string
}

func (d daemonBridgeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	forwarded := request.Clone(request.Context())
	forwarded.URL.Host = d.address
	return d.base.RoundTrip(forwarded)
}

func TestLegacyProxyMissingCapabilityRemainsToolError(t *testing.T) {
	srv, _ := proxyErrorServer(t, missingFormCapability())
	response := serve(t, srv, callTool("svc__confirm", map[string]any{}))
	result, ok := response["result"].(map[string]any)
	if response["error"] != nil || !ok || result["isError"] != true {
		t.Fatalf("legacy response = %v, want isError tool result", response)
	}
	if text := toolResultText(
		t,
		response,
	); !strings.Contains(text, `"error":"tool_error"`) ||
		!strings.Contains(text, missingFormCapability().Message) {
		t.Fatalf("legacy error text = %s, want existing tool_error envelope", text)
	}
}

func TestModernProxyOtherRPCErrorRemainsToolError(t *testing.T) {
	srv, _ := proxyErrorServer(
		t,
		&transport.RPCError{Code: transport.CodeInvalidParams, Message: "bad upstream arguments"},
	)
	request := transporttest.ModernRequest(
		41,
		"tools/call",
		transport.ToolCallParams{Name: "svc__confirm", Arguments: map[string]any{}},
	)
	response := responsesByID(serveModern(t, srv, request))[41]
	if response.Error != nil {
		t.Fatalf("unexpected forwarded RPC error: %+v", response.Error)
	}
	result := decodeResult[transport.ToolCallResult](t, response)
	if !result.IsError || len(result.Content) != 1 ||
		!strings.Contains(result.Content[0].Text, "bad upstream arguments") {
		t.Fatalf("result = %s, want existing tool error with the upstream message", response.Result)
	}
}

func TestModernProxyToolExecutionErrorRemainsResult(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	conn := fakeConn("confirm")
	result := json.RawMessage(
		`{"resultType":"complete","isError":true,"content":[{"type":"text","text":"confirmation failed"}],"_meta":{"upstream":"keep"}}`,
	)
	conn.Responses["tools/call"] = result
	addProxyConn(addProxyConnParams{T: t, Server: srv, Name: "svc", Connection: conn})
	request := transporttest.ModernRequest(
		41, "tools/call", transport.ToolCallParams{Name: "svc__confirm", Arguments: map[string]any{}},
	)
	response := responsesByID(serveModern(t, srv, request))[41]
	if response.Error != nil || !bytes.Equal(response.Result, result) {
		t.Fatalf("response = %+v, want the upstream tool execution error unchanged", response)
	}
}

func TestModernProxyPreservesHTTPMissingCapabilityError(t *testing.T) {
	upstream := &capabilityErrorHTTPServer{t: t}
	endpoint := httptest.NewServer(upstream)
	t.Cleanup(endpoint.Close)
	cfg := config.DefaultConfig()
	cfg.DangerousAllowPrivateURLs = true
	srv := newTestServer(t, server.Params{Config: cfg})
	srv.ConnectUpstreams(t.Context(), []config.ServerConfig{{Name: "svc", Transport: "http", URL: endpoint.URL}})
	serveModern(t, srv, transporttest.ModernRequest(0, "tools/list", nil))
	id := json.RawMessage(`"agent-request"`)
	request := transporttest.ModernRequest(
		id,
		"tools/call",
		transport.ToolCallParams{Name: "svc__confirm", Arguments: map[string]any{}},
	)
	requireMissingCapabilityResponse(t, serveModern(t, srv, request), id)
	if got := upstream.calls.Load(); got != 1 {
		t.Fatalf("HTTP tool calls = %d, want 1 without automatic retry", got)
	}
}

type capabilityErrorHTTPServer struct {
	t     *testing.T
	calls atomic.Int32
}

func (s *capabilityErrorHTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.t.Errorf("decode upstream request: %v", err)
		return
	}
	if request.Method == "tools/call" {
		s.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		response := transport.Response{
			JsonRPC: transport.JsonRPCVersion,
			ID:      request.ID,
			Error:   missingFormCapability(),
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			s.t.Errorf("write upstream capability error: %v", err)
		}
		return
	}
	writeUpstreamResult(
		writeUpstreamResultParams{T: s.t, Writer: w, ID: request.ID, Result: capabilityDiscoveryResult(request.Method)},
	)
}

func capabilityDiscoveryResult(method string) any {
	switch method {
	case "server/discover":
		return transport.DiscoverResult{
			ResultType:        "complete",
			SupportedVersions: transport.ModernVersions,
			CacheScope:        "private",
		}
	case "tools/list":
		return map[string]any{
			"resultType": "complete",
			"ttlMs":      0,
			"cacheScope": "private",
			"tools": []transport.ToolDefinition{
				{Name: "confirm", InputSchema: json.RawMessage(`{"type":"object"}`)},
			},
		}
	}
	return map[string]any{"resultType": "complete"}
}
