//go:build test

package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

func TestInjectCompactMode_initialize_addsFlag(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{}}}`)
	got := injectCompactMode(line)
	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(got, &msg); err != nil {
		t.Fatalf("unmarshal result: %v\ngot: %s", err, got)
	}
	if string(msg.Params["_mini_tool_mode"]) != `"compact"` {
		t.Errorf("_mini_tool_mode = %s, want \"compact\"", msg.Params["_mini_tool_mode"])
	}
}

func TestInjectCompactMode_initialize_preservesExistingParams(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"test"}}}`)
	got := injectCompactMode(line)
	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(got, &msg); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if string(msg.Params["protocolVersion"]) != `"2025-03-26"` {
		t.Errorf("protocolVersion lost: %s", msg.Params["protocolVersion"])
	}
	if msg.Params["clientInfo"] == nil {
		t.Error("clientInfo lost after injection")
	}
}

func TestInjectCompactMode_nonInitialize_unchanged(t *testing.T) {
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"foo"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	}
	for _, line := range lines {
		if got := injectCompactMode([]byte(line)); string(got) != line {
			t.Errorf("non-initialize message modified:\nwant: %s\n got: %s", line, got)
		}
	}
}

func TestInjectCompactMode_malformedJSON_unchanged(t *testing.T) {
	line := []byte(`not valid json`)
	if got := injectCompactMode(line); string(got) != string(line) {
		t.Errorf("malformed JSON should be returned unchanged, got: %s", got)
	}
}

func TestInjectCompactMode_initialize_noParams_addsFlag(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	got := injectCompactMode(line)
	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(got, &msg); err != nil {
		t.Fatalf("unmarshal result: %v\ngot: %s", err, got)
	}
	if string(msg.Params["_mini_tool_mode"]) != `"compact"` {
		t.Errorf("_mini_tool_mode = %s, want \"compact\"", msg.Params["_mini_tool_mode"])
	}
}

func TestRun_compact_injectsIntoInitialize(t *testing.T) {
	params, got := runInitializeWithMode(t, transport.ToolModeCompact)
	if string(params["_mini_tool_mode"]) != `"compact"` {
		t.Errorf("daemon did not receive compact mode; params: %v, body: %s", params, got)
	}
}

func TestRun_proxy_doesNotInjectFlag(t *testing.T) {
	params, _ := runInitializeWithMode(t, transport.ToolModeProxy)
	if params["_mini_tool_mode"] != nil {
		t.Errorf("proxy mode should not inject _mini_tool_mode; params: %v", params)
	}
}

func runInitializeWithMode(t *testing.T, mode transport.ToolMode) (map[string]json.RawMessage, []byte) {
	t.Helper()
	var gotBody []byte
	client := serveSocket(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	})
	initMsg := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}` + "\n"
	p := RunParams{
		Client: client, SessionID: "sess", In: strings.NewReader(initMsg), Out: io.Discard,
		ToolMode: mode, Clock: clock.NewFake(),
	}
	if err := Run(p); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	var msg struct {
		Params map[string]json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(gotBody, &msg); err != nil {
		t.Fatalf("unmarshal forwarded body: %v\nbody: %s", err, gotBody)
	}
	return msg.Params, gotBody
}
