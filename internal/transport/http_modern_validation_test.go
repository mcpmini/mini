//go:build test

package transport

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
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

func TestModernLegacyAnnotatedToolsKeepLegacyWireShape(t *testing.T) {
	var sawAnnotated bool
	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case "server/discover":
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: DiscoverResult{SupportedVersions: LegacyVersions},
			})
		case "initialize":
			serveProbeCall(t, w, req)
		case NotificationInitialized:
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			tool := modernTool("x", `{"type":"object","properties":{"p":{"type":"string","x-mcp-header":"P"}}}`)
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: ToolsListResult{Tools: []ToolDefinition{tool}},
			})
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
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: map[string]bool{"ok": true},
			})
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

const ownerHeaderSchema = `{"type":"object","properties":{"owner":{"type":"string","x-mcp-header":"Owner"}}}`

func TestModernPartialListingStillSendsToolHeaders(t *testing.T) {
	callHeaders := make(chan http.Header, 1)
	server := newModernServer(t, modernUpstream{Handle: failSecondToolsPage(t, callHeaders)})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
	if tools, err := conn.ListTools(t.Context()); err != nil || len(tools) != 1 {
		t.Fatalf("ListTools = %v, %v; want the first page's tool", tools, err)
	}
	if _, err := conn.Call(
		t.Context(),
		"tools/call",
		json.RawMessage(`{"name":"x","arguments":{"owner":"octo"}}`),
	); err != nil {
		t.Fatal(err)
	}
	if got := (<-callHeaders).Get("Mcp-Param-Owner"); got != "octo" {
		t.Fatalf("Mcp-Param-Owner = %q after a partial listing, want octo", got)
	}
}

func failSecondToolsPage(t *testing.T, callHeaders chan<- http.Header) upstreamHandler {
	return func(w http.ResponseWriter, r *http.Request, req Request) {
		switch {
		case req.Method == "tools/list" && !bytes.Contains(req.Params, []byte(`"cursor"`)):
			page := ToolsListResult{Tools: []ToolDefinition{modernTool("x", ownerHeaderSchema)}, NextCursor: "2"}
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: page,
			})
		case req.Method == "tools/list":
			writeHTTPRPCError(t, w, httpRPCErrorParams{id: req.ID, code: CodeInvalidParams})
		case req.Method == "tools/call":
			callHeaders <- r.Header.Clone()
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: map[string]bool{"ok": true},
			})
		}
	}
}
