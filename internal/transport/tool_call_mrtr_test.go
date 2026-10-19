//go:build test

package transport

import (
	"bufio"
	"encoding/json"
	"net/http"
	"testing"
)

const modernToolCallParams = `{"name":"confirm","arguments":{"x":1},` +
	`"_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{}}},` +
	`"inputResponses":{"r1":{"action":"accept"}},"requestState":"opaque-123"}`

func TestStripModernToolCallFields(t *testing.T) {
	cases := []struct {
		name   string
		params string
		want   string
	}{
		{
			name:   "plain params keep their bytes",
			params: `{"name":"confirm","arguments":{"x":1}}`,
			want:   `{"name":"confirm","arguments":{"x":1}}`,
		},
		{
			name:   "all modern fields removed",
			params: modernToolCallParams,
			want:   `{"arguments":{"x":1},"name":"confirm"}`,
		},
		{
			name: "other meta keys kept",
			params: `{"name":"confirm","_meta":{"progressToken":7,` +
				`"io.modelcontextprotocol/clientCapabilities":{"elicitation":{}}}}`,
			want: `{"_meta":{"progressToken":7},"name":"confirm"}`,
		},
		{
			name:   "meta without capabilities keeps its bytes",
			params: `{"name":"confirm","_meta":{"progressToken":7}}`,
			want:   `{"name":"confirm","_meta":{"progressToken":7}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripModernToolCallFields(json.RawMessage(tc.params)); string(got) != tc.want {
				t.Fatalf("stripped params = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestNewToolCallParamsCarriesOnlyElicitation(t *testing.T) {
	params := NewToolCallParams("confirm", map[string]any{}, &ToolCallMRTR{
		Elicitation:    json.RawMessage(`{"form":{}}`),
		InputResponses: json.RawMessage(`{"r1":{}}`),
		RequestState:   json.RawMessage(`"opaque-123"`),
	})
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"confirm","arguments":{},` +
		`"_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{"form":{}}}},` +
		`"inputResponses":{"r1":{}},"requestState":"opaque-123"}`
	if string(encoded) != want {
		t.Fatalf("tool call params = %s, want %s", encoded, want)
	}
}

func TestLegacyHTTPToolCallDropsModernFields(t *testing.T) {
	var got json.RawMessage
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case "initialize":
			writeRPCResult(t, w, req.ID, InitializeResult{ProtocolVersion: LegacyVersions[0]})
		case NotificationInitialized:
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			got = req.Params
			writeRPCResult(t, w, req.ID, map[string]any{"content": []any{}})
		}
	})
	conn := mustLegacyHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	if _, err := conn.Call(t.Context(), "tools/call", json.RawMessage(modernToolCallParams)); err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"arguments":{"x":1},"name":"confirm"}` {
		t.Fatalf("legacy upstream received %s", got)
	}
}

func TestStdioToolCallDropsModernFields(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	received := make(chan Request, 1)
	go func() {
		scanner := bufio.NewScanner(serverR)
		if !scanner.Scan() {
			return
		}
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			t.Errorf("decode stdio request: %v", err)
			return
		}
		received <- req
		sendResponse(serverW, 1, map[string]any{"content": []any{}})
	}()
	if _, err := conn.Call(t.Context(), "tools/call", json.RawMessage(modernToolCallParams)); err != nil {
		t.Fatal(err)
	}
	if got := (<-received).Params; string(got) != `{"arguments":{"x":1},"name":"confirm"}` {
		t.Fatalf("stdio upstream received %s", got)
	}
}
