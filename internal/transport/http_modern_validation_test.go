//go:build test

package transport

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
)

func TestModernToolHeaderValidationDropsOnlyInvalidDefinitions(t *testing.T) {
	tools := []ToolDefinition{
		modernTool("number", `{"type":"object","properties":{"p":{"type":"number","x-mcp-header":"P"}}}`),
		modernTool("valid after number", `{"type":"object","properties":{}}`),
		modernTool("missing type", `{"type":"object","properties":{"p":{"x-mcp-header":"P"}}}`),
		modernTool("valid after missing type", `{"type":"object","properties":{}}`),
		modernTool("type list", `{"type":"object","properties":{"p":{"type":["string"],"x-mcp-header":"P"}}}`),
		modernTool("valid after type list", `{"type":"object","properties":{}}`),
		modernTool("space", `{"type":"object","properties":{"p":{"type":"string","x-mcp-header":"bad name"}}}`),
		modernTool("valid after space", `{"type":"object","properties":{}}`),
		modernTool("duplicate", `{"type":"object","properties":{
			"a":{"type":"string","x-mcp-header":"X"},
			"b":{"type":"integer","x-mcp-header":"x"}}}`),
		modernTool("valid after duplicate", `{"type":"object","properties":{}}`),
		modernTool("items", `{"type":"object","properties":{"rows":{"type":"array",
			"items":{"type":"string","x-mcp-header":"P"}}}}`),
		modernTool("valid after items", `{"type":"object","properties":{}}`),
		modernTool("root", `{"type":"string","x-mcp-header":"P"}`),
		modernTool("valid after root", `{"type":"object","properties":{}}`),
		modernTool("anyOf", `{"type":"object","properties":{"p":{"anyOf":[{"type":"string","x-mcp-header":"P"}]}}}`),
		modernTool("valid after anyOf", `{"type":"object","properties":{"x-mcp-header":{"type":"string"}}}`),
	}
	conn := &HTTPConnection{serverName: "test"}
	got := conn.rebuildToolHeaders(tools)
	want := []string{
		"valid after number", "valid after missing type", "valid after type list",
		"valid after space", "valid after duplicate", "valid after items", "valid after root", "valid after anyOf",
	}
	if len(got) != len(want) {
		t.Fatalf("valid tool count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("tool[%d] = %q, want %q", i, got[i].Name, want[i])
		}
	}
	if _, ok := conn.modernHeaders["valid after anyOf"]; !ok {
		t.Fatal("property named x-mcp-header invalidated its tool")
	}
}

func TestModernHeaderMismatchRelistsAndRetriesOnce(t *testing.T) {
	var lists, calls atomic.Int32
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case "server/discover":
			writeRPCResult(t, w, req.ID, DiscoverResult{SupportedVersions: ModernVersions})
		case "tools/list":
			list := lists.Add(1)
			name := "Old"
			if list == 2 {
				name = "New"
			}
			tool := modernTool("x", `{"type":"object","properties":{"p":{"type":"string","x-mcp-header":"`+name+`"}}}`)
			writeRPCResult(t, w, req.ID, ToolsListResult{Tools: []ToolDefinition{tool}})
		case "tools/call":
			call := calls.Add(1)
			want := "Old"
			if call == 2 {
				want = "New"
			}
			if got := r.Header.Get("Mcp-Param-" + want); got != "new" {
				t.Errorf("retry header %s = %q", want, got)
			}
			if call == 1 {
				writeHeaderMismatch(t, w, req.ID)
				return
			}
			writeRPCResult(t, w, req.ID, map[string]bool{"ok": true})
		}
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
	_, err := conn.Call(t.Context(), "tools/call", json.RawMessage(`{"name":"x","arguments":{"p":"new"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if lists.Load() != 2 || calls.Load() != 2 {
		t.Fatalf("lists/calls = %d/%d, want 2/2", lists.Load(), calls.Load())
	}
}

func TestModernHeaderMismatchStopsAfterRetry(t *testing.T) {
	var calls atomic.Int32
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case "server/discover":
			writeRPCResult(t, w, req.ID, DiscoverResult{SupportedVersions: ModernVersions})
		case "tools/list":
			tool := modernTool("x", `{"type":"object","properties":{}}`)
			writeRPCResult(t, w, req.ID, ToolsListResult{Tools: []ToolDefinition{tool}})
		case "tools/call":
			calls.Add(1)
			writeHeaderMismatch(t, w, req.ID)
		}
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
	_, err := conn.Call(t.Context(), "tools/call", json.RawMessage(`{"name":"x","arguments":{}}`))
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32020 {
		t.Fatalf("error = %v, want -32020 RPCError", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestModernLegacyAnnotatedToolsKeepLegacyWireShape(t *testing.T) {
	var sawAnnotated bool
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case "server/discover":
			writeRPCResult(t, w, req.ID, DiscoverResult{SupportedVersions: LegacyVersions})
		case "initialize":
			serveProbeCall(t, w, req)
		case NotificationInitialized:
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			tool := modernTool("x", `{"type":"object","properties":{"p":{"type":"string","x-mcp-header":"P"}}}`)
			writeRPCResult(t, w, req.ID, ToolsListResult{Tools: []ToolDefinition{tool}})
		case "tools/call":
			sawAnnotated = true
			if strings.Contains(string(req.Params), `"_meta"`) {
				t.Error("legacy params gained _meta")
			}
			for _, key := range []string{"Mcp-Method", "Mcp-Name", "Mcp-Param-P"} {
				if r.Header.Get(key) != "" {
					t.Errorf("legacy request included %s", key)
				}
			}
			writeRPCResult(t, w, req.ID, map[string]bool{"ok": true})
		}
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
	tools, err := conn.ListTools(t.Context())
	if err != nil || len(tools) != 1 || tools[0].Name != "x" {
		t.Fatalf("legacy ListTools = %v, %v", tools, err)
	}
	callParams := json.RawMessage(`{"name":"x","arguments":{"p":"v"}}`)
	if _, err := conn.Call(t.Context(), "tools/call", callParams); err != nil {
		t.Fatal(err)
	}
	if !sawAnnotated {
		t.Fatal("annotated legacy tool was not called")
	}
}

func rpcErrorUntilReprobe(t *testing.T, code int) (*HTTPConnection, *atomic.Int32) {
	t.Helper()
	var probes atomic.Int32
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case "server/discover":
			probes.Add(1)
			writeRPCResult(t, w, req.ID, DiscoverResult{SupportedVersions: ModernVersions})
		case "ping":
			if probes.Load() == 1 {
				writeHTTPRPCError(t, w, httpRPCErrorParams{id: req.ID, code: code})
			} else {
				writeRPCResult(t, w, req.ID, map[string]bool{"ok": true})
			}
		}
	})
	return mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()}), &probes
}

func TestModernUnsupportedVersionErrorResetsDiscovery(t *testing.T) {
	conn, probes := rpcErrorUntilReprobe(t, CodeUnsupportedProtocolVersion)
	_, err := conn.Call(t.Context(), "ping", nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeUnsupportedProtocolVersion {
		t.Fatalf("first error = %v", err)
	}
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil || probes.Load() != 2 {
		t.Fatalf("call after reset: %v, probes=%d, want 2", err, probes.Load())
	}
}

func TestModernOtherRPCErrorKeepsDiscovery(t *testing.T) {
	conn, probes := rpcErrorUntilReprobe(t, CodeInvalidParams)
	for range 2 {
		_, err := conn.Call(t.Context(), "ping", nil)
		var rpcErr *RPCError
		if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInvalidParams {
			t.Fatalf("ping error = %v, want the upstream's -32602", err)
		}
	}
	if probes.Load() != 1 {
		t.Fatalf("probes = %d after two -32602 errors, want 1", probes.Load())
	}
}

func TestModern4xxWithoutRPCResetsDiscovery(t *testing.T) {
	var probes atomic.Int32
	srv := newJSONRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case "server/discover":
			probes.Add(1)
			writeRPCResult(t, w, req.ID, DiscoverResult{SupportedVersions: ModernVersions})
		case "ping":
			if probes.Load() == 1 {
				w.WriteHeader(http.StatusNotFound)
				if _, writeErr := w.Write([]byte("<html>missing</html>")); writeErr != nil {
					t.Errorf("write HTML error body: %v", writeErr)
				}
				return
			}
			writeRPCResult(t, w, req.ID, map[string]bool{"ok": true})
		}
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clock.NewFake()})
	_, err := conn.Call(t.Context(), "ping", nil)
	if !IsConnectionError(err) {
		t.Fatalf("404 error = %T %v, want ConnectionError", err, err)
	}
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil || probes.Load() != 2 {
		t.Fatalf("call after 404 reset: %v, probes=%d", err, probes.Load())
	}
}
