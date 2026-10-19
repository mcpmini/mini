//go:build test

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
)

func TestModernDiscoverToolsListAndCallWithoutInitialize(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	conn := fakeConn("ping")
	conn.Responses["tools/call"] = json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)
	if err := srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	msgs := serveModern(t, srv,
		modernRPC("server/discover", 1, nil),
		modernRPC("tools/list", 2, nil),
		modernRPC("tools/call", 3, map[string]any{"name": "svc__ping", "arguments": map[string]any{}}),
	)
	results := responsesByID(msgs)
	for _, id := range []string{"1", "2", "3"} {
		result, _ := results[id]["result"].(map[string]any)
		if result["resultType"] != "complete" {
			t.Errorf("response %s resultType = %v", id, result["resultType"])
		}
	}
	list := results["2"]["result"].(map[string]any)
	if list["ttlMs"] != float64(0) || list["cacheScope"] != "private" {
		t.Errorf("tools/list cache fields = %v", list)
	}
	if results["3"]["error"] != nil {
		t.Fatalf("modern tools/call failed: %v", results["3"]["error"])
	}
	discover := results["1"]["result"].(map[string]any)
	if discover["supportedVersions"] == nil || discover["_meta"] == nil || discover["ttlMs"] != float64(0) {
		t.Errorf("incomplete discovery result: %v", discover)
	}
	capabilities := discover["capabilities"].(map[string]any)
	if capabilities["tools"].(map[string]any)["listChanged"] != true {
		t.Errorf("discovery capabilities = %v", capabilities)
	}
	meta := discover["_meta"].(map[string]any)
	if meta["io.modelcontextprotocol/serverInfo"].(map[string]any)["name"] != "mini" {
		t.Errorf("discovery serverInfo = %v", meta)
	}
}

func TestModernCompactModeWithoutInitialize(t *testing.T) {
	srv := newTestServer(t, server.Params{ToolMode: transport.ToolModeCompact})
	msgs := serveModern(t, srv, modernRPC("tools/list", 1, nil))
	result := responsesByID(msgs)["1"]["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 4 || result["resultType"] != "complete" {
		t.Fatalf("compact tools/list result = %v", result)
	}
}

func TestModernUnsupportedVersion(t *testing.T) {
	msgs := serveModern(t, newTestServer(t, server.Params{}), modernRPCVersion("tools/list", 7, "1900-01-01", nil))
	errObj := responsesByID(msgs)["7"]["error"].(map[string]any)
	if errObj["code"] != float64(-32022) || errObj["message"] != "Unsupported protocol version" {
		t.Fatalf("unsupported version error = %v", errObj)
	}
	data := errObj["data"].(map[string]any)
	if data["requested"] != "1900-01-01" ||
		!sameVersions(
			data["supported"].([]any),
			append(append([]string{}, transport.ModernVersions...), transport.LegacyVersions...),
		) {
		t.Fatalf("unsupported version data = %v", data)
	}
}

func sameVersions(got []any, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestModernLegacyDiscoverRemainsMethodNotFound(t *testing.T) {
	msgs := serveAll(t, newTestServer(t, server.Params{}), rpc("server/discover", nil))
	if got := rpcErrorCode(responsesByID(msgs)["1"]); got != transport.CodeMethodNotFound {
		t.Fatalf("legacy server/discover code = %v", got)
	}
}

func TestModernDiscoverOverHTTPRemainsMethodNotFound(t *testing.T) {
	srv := newTestServer(t, server.Params{})
	init := httptest.NewRequest("POST", "http://127.0.0.1/mcp", bytes.NewReader(initRequest()))
	init.Header.Set("Content-Type", "application/json")
	init.Host = "127.0.0.1"
	initResponse := httptest.NewRecorder()
	srv.ServeHTTP(initResponse, init)
	sessionID := initResponse.Header().Get("Mcp-Session-Id")
	req := httptest.NewRequest("POST", "http://127.0.0.1/mcp", bytes.NewReader(modernRPC("server/discover", 77, nil)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Session-Id", sessionID)
	req.Host = "127.0.0.1"
	response := httptest.NewRecorder()
	srv.ServeHTTP(response, req)
	var message map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &message); err != nil {
		t.Fatal(err)
	}
	if got := rpcErrorCode(message); got != transport.CodeMethodNotFound {
		t.Fatalf("HTTP server/discover code = %v, want %d", got, transport.CodeMethodNotFound)
	}
}

func serveModern(t *testing.T, srv *server.Server, lines ...[]byte) []map[string]any {
	t.Helper()
	input := bytes.Join(lines, []byte{})
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Serve(ctx, bytes.NewReader(input), &out); err != nil {
		t.Fatalf("Serve modern requests: %v", err)
	}
	return parseMessages(out.Bytes())
}

func modernRPC(method string, id any, params any) []byte {
	return modernRPCVersion(method, id, transport.ModernVersions[0], params)
}

func modernRPCVersion(method string, id any, version string, params any) []byte {
	fields := map[string]any{"_meta": map[string]any{
		"io.modelcontextprotocol/protocolVersion":    version,
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "test", "version": "0"},
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
	}}
	if params != nil {
		for key, value := range params.(map[string]any) {
			fields[key] = value
		}
	}
	encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": fields})
	if err != nil {
		panic(err)
	}
	return append(encoded, '\n')
}

func responsesByID(messages []map[string]any) map[string]map[string]any {
	responses := make(map[string]map[string]any)
	for _, message := range messages {
		if id, ok := message["id"]; ok {
			responses[jsonID(id)] = message
		}
	}
	return responses
}

func findMethod(messages []map[string]any, method string) map[string]any {
	for _, message := range messages {
		if message["method"] == method {
			return message
		}
	}
	return nil
}

func jsonID(id any) string {
	encoded, err := json.Marshal(id)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
