//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/transport/transporttest"
)

func TestModernDiscoverToolsListAndCallWithoutInitialize(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	conn := fakeConn("ping")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	responses := responsesByID(serveModern(
		t,
		srv,
		transporttest.ModernRequest(1, "server/discover", nil),
		transporttest.ModernRequest(2, "tools/list", nil),
		transporttest.ModernRequest(
			3,
			"tools/call",
			transport.ToolCallParams{Name: "svc__ping", Arguments: map[string]any{}},
		),
	))
	for id := 1; id <= 3; id++ {
		if result := decodeResult[modernResultFields](t, responses[id]); result.ResultType != transport.ResultComplete {
			t.Errorf("response %d resultType = %q", id, result.ResultType)
		}
	}
	list := decodeResult[modernResultFields](t, responses[2])
	if list.TTLMs == nil || *list.TTLMs != 0 || list.CacheScope != "private" {
		t.Errorf("tools/list cache fields = %s", responses[2].Result)
	}
	discover := decodeResult[transport.DiscoverResult](t, responses[1])
	if len(discover.SupportedVersions) == 0 || discover.Meta == nil || discover.Meta.ServerInfo.Name != "mini" ||
		!discover.Capabilities.ToolsListChanged() {
		t.Errorf("incomplete discovery result: %s", responses[1].Result)
	}
}

func TestModernUnsupportedVersion(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	response := responsesByID(serveModern(t, srv, transporttest.ModernRequestVersion(transporttest.ModernRequestVersionParams{
		Version: "1900-01-01",
		ID:      7,
		Method:  "tools/list",
		Params:  nil,
	})))[7]
	if response.Error == nil || response.Error.Code != transport.CodeUnsupportedProtocolVersion ||
		response.Error.Message != "Unsupported protocol version" {
		t.Fatalf("unsupported version error = %+v", response.Error)
	}
	var data transport.UnsupportedVersionData
	if err := json.Unmarshal(response.Error.Data, &data); err != nil {
		t.Fatal(err)
	}
	supported := slices.Concat(transport.ModernVersions, transport.LegacyVersions)
	if data.Requested != "1900-01-01" || !slices.Equal(data.Supported, supported) {
		t.Fatalf("unsupported version data = %+v", data)
	}
}

func TestModernRejectedVersionLeavesTheSessionFreeToInitialize(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	responses := responsesByID(serveModern(t, srv,
		transporttest.ModernRequestVersion(transporttest.ModernRequestVersionParams{
			Version: "1900-01-01",
			ID:      1,
			Method:  "server/discover",
			Params:  nil,
		}),
		legacyRequest(legacyRequestParams{T: t, ID: 2, Method: "initialize", Params: initParams()}),
		legacyRequest(legacyRequestParams{T: t, ID: 3, Method: "tools/list", Params: nil}),
	))
	if responses[1].Error == nil || responses[1].Error.Code != transport.CodeUnsupportedProtocolVersion {
		t.Fatalf("discover with an unsupported version = %+v, want -32022", responses[1].Error)
	}
	if tools := decodeResult[transport.ToolsListResult](t, responses[3]).Tools; len(tools) != 2 {
		t.Fatalf("initialize after the rejected probe listed %d tools, want config and read", len(tools))
	}
}

type legacyRequestParams struct {
	T      *testing.T
	ID     int
	Method string
	Params any
}

func legacyRequest(params legacyRequestParams) []byte {
	params.T.Helper()
	encoded, err := json.Marshal(params.Params)
	if err != nil {
		params.T.Fatal(err)
	}
	line, err := json.Marshal(
		transport.Request{JsonRPC: transport.JsonRPCVersion, ID: params.ID, Method: params.Method, Params: encoded},
	)
	if err != nil {
		params.T.Fatal(err)
	}
	return append(line, '\n')
}

func TestModernLegacyDiscoverRemainsMethodNotFound(t *testing.T) {
	msgs := serveAll(t, newTestServer(t, server.Params{}), rpc("server/discover", nil))
	if got := rpcErrorCode(msgs[len(msgs)-1]); got != transport.CodeMethodNotFound {
		t.Fatalf("legacy server/discover code = %v", got)
	}
}

// modernMessage holds any JSON-RPC message mini writes: a response or a notification.
type modernMessage struct {
	ID     json.RawMessage     `json:"id,omitempty"`
	Method string              `json:"method,omitempty"`
	Params json.RawMessage     `json:"params,omitempty"`
	Result json.RawMessage     `json:"result,omitempty"`
	Error  *transport.RPCError `json:"error,omitempty"`
}

func (m modernMessage) isResponseTo(id int) bool {
	return m.Method == "" && string(m.ID) == strconv.Itoa(id)
}

// modernResultFields are the fields mini adds to every result for a modern client.
type modernResultFields struct {
	ResultType transport.ResultType `json:"resultType"`
	TTLMs      *int                 `json:"ttlMs"`
	CacheScope string               `json:"cacheScope"`
}

func decodeResult[T any](t *testing.T, response modernMessage) T {
	t.Helper()
	var result T
	if response.Error != nil {
		t.Fatalf("response %s failed: %+v", response.ID, response.Error)
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode result %s: %v", response.Result, err)
	}
	return result
}

func decodeParams[T any](t *testing.T, message modernMessage) T {
	t.Helper()
	var params T
	if err := json.Unmarshal(message.Params, &params); err != nil {
		t.Fatalf("decode %s params %s: %v", message.Method, message.Params, err)
	}
	return params
}

func serveModern(t *testing.T, srv *server.Server, lines ...[]byte) []modernMessage {
	t.Helper()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Serve(ctx, bytes.NewReader(bytes.Join(lines, nil)), &out); err != nil {
		t.Fatalf("Serve modern requests: %v", err)
	}
	var messages []modernMessage
	for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n")) {
		messages = append(messages, decodeMessage(t, line))
	}
	return messages
}

func decodeMessage(t *testing.T, line []byte) modernMessage {
	t.Helper()
	var message modernMessage
	if err := json.Unmarshal(line, &message); err != nil {
		t.Fatalf("decode message %s: %v", line, err)
	}
	return message
}

func responsesByID(messages []modernMessage) map[int]modernMessage {
	responses := make(map[int]modernMessage)
	for _, message := range messages {
		if id, err := strconv.Atoi(string(message.ID)); err == nil && message.Method == "" {
			responses[id] = message
		}
	}
	return responses
}

func TestModernMetadataRejectedBeforeActivationOrDispatch(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	conn := fakeConn("ping")
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	callParams := json.RawMessage(`{"name":"svc__ping","arguments":{},"_meta":null}`)
	lines := [][]byte{
		legacyRequest(legacyRequestParams{
			T:  t,
			ID: 1, Method: "server/discover",
			Params: json.RawMessage(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`),
		}),
		legacyRequest(legacyRequestParams{
			T:  t,
			ID: 2, Method: "tools/list",
			Params: json.RawMessage(`{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
				`"io.modelcontextprotocol/clientCapabilities":[]}}`),
		}),
		legacyRequest(legacyRequestParams{T: t, ID: 3, Method: "tools/call", Params: callParams}),
		legacyRequest(legacyRequestParams{T: t, ID: 4, Method: "initialize", Params: initParams()}),
		legacyRequest(legacyRequestParams{T: t, ID: 5, Method: "tools/list", Params: nil}),
	}
	responses := responsesByID(serveModern(t, srv, lines...))
	for _, id := range []int{1, 2, 3} {
		if responses[id].Error == nil || responses[id].Error.Code != transport.CodeInvalidParams {
			t.Errorf("request %d error = %+v, want -32602", id, responses[id].Error)
		}
	}
	if got := len(decodeResult[transport.ToolsListResult](t, responses[5]).Tools); got != 3 {
		t.Fatalf("legacy tools/list after invalid probes returned %d tools, want config, read and svc__ping", got)
	}
	if len(conn.LastParams) != 0 {
		t.Fatalf("invalid tools/call reached upstream with params %s", conn.LastParams)
	}
}

const listResultWithText = `{"content":[{"type":"text","text":"[{\"id\":1,\"secret\":\"x\"}]"}],` +
	`"structuredContent":[{"id":1,"secret":"x"}]}`

func TestLegacyToolCall_ListStructuredContentIsDroppedAndTextKept(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", listResultWithText, "list")

	result := h.call("svc__list", nil)

	if _, ok := result["structuredContent"]; ok {
		t.Errorf("legacy agent got non-object structuredContent: %v", result)
	}
	if texts := contentTexts(result); len(texts) != 1 || texts[0] != `[{"id":1,"secret":"x"}]` {
		t.Errorf("want the upstream text block unchanged, got %q", texts)
	}
}

func TestLegacyToolCall_ListStructuredContentWithoutTextBecomesText(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", `{"content":[],"structuredContent":[1,2]}`, "list")

	result := h.call("svc__list", nil)

	if _, ok := result["structuredContent"]; ok {
		t.Errorf("legacy agent got non-object structuredContent: %v", result)
	}
	if texts := contentTexts(result); len(texts) != 1 || texts[0] != "[1,2]" {
		t.Errorf("want the list as a text block, got %q", texts)
	}
}

func TestLegacyToolCall_ToonFormat_ListIsNotAddedAgainAsJSON(t *testing.T) {
	h := newProxyHarness(t, newProxyToonServer(t, nil))
	h.addUpstream("svc", listResultWithText, "list")

	result := h.call("svc__list", nil)

	if _, ok := result["structuredContent"]; ok {
		t.Errorf("legacy agent got non-object structuredContent: %v", result)
	}
	if texts := contentTexts(result); len(texts) != 1 || strings.Contains(texts[0], `"id"`) {
		t.Errorf("want only the TOON text block, got %q", texts)
	}
}

func TestLegacyToolCall_TrimmedListKeepsNoticeInText(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", listResultWithText, "list")
	h.setProjection("svc", "list", map[string]any{"exclude": []string{"[].secret"}})

	result := h.call("svc__list", nil)

	if _, ok := result["structuredContent"]; ok {
		t.Errorf("legacy agent got non-object structuredContent: %v", result)
	}
	texts := contentTexts(result)
	if len(texts) != 1 || !strings.Contains(texts[0], `"__mini"`) || strings.Contains(texts[0], `"secret":`) {
		t.Errorf("want one trimmed text block carrying the notice, got %q", texts)
	}
}

func TestLegacyToolCall_ObjectStructuredContentPassesThrough(t *testing.T) {
	h := newProxyHarness(t, newTestServer(t, server.Params{}))
	h.addUpstream("svc", `{"content":[{"type":"text","text":"{\"id\":1}"}],"structuredContent":{"id":1}}`, "get")

	structured, ok := h.call("svc__get", nil)["structuredContent"].(map[string]any)

	if !ok || structured["id"] != float64(1) {
		t.Errorf("object structuredContent must reach a legacy agent, got %v", structured)
	}
}

func TestModernToolCall_ListStructuredContentPassesThrough(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	conn := fakeConn("list")
	conn.Responses["tools/call"] = json.RawMessage(listResultWithText)
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	call := transport.ToolCallParams{Name: "svc__list", Arguments: map[string]any{}}

	response := responsesByID(serveModern(t, srv, transporttest.ModernRequest(1, "tools/call", call)))[1]

	result := decodeResult[struct {
		StructuredContent []map[string]any `json:"structuredContent"`
	}](t, response)
	if len(result.StructuredContent) != 1 {
		t.Errorf("modern agent must get the list structuredContent, got %s", response.Result)
	}
}
