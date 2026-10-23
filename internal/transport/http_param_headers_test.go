//go:build test

package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
)

func TestModernRequestCarriesMetadataAndParameterHeaders(t *testing.T) {
	var methods []string
	var gotParams json.RawMessage
	var callCount atomic.Int32
	schema := `{"type":"object","properties":{
		"owner":{"type":"string","x-mcp-header":"Owner"},
		"repo":{"type":"string","x-mcp-header":"Repo"},
		"enabled":{"type":"boolean","x-mcp-header":"Enabled"},
		"a":{"type":"object","properties":{"b":{"type":"integer","x-mcp-header":"Nested"}}}}}`
	tool := modernTool("tool name", schema)
	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		methods = append(methods, req.Method)
		assertModernHeader(t, modernHeaderParams{
			Request: r,
			Name:    "Mcp-Method",
			Want:    req.Method,
		})
		assertModernHeader(t, modernHeaderParams{
			Request: r,
			Name:    "MCP-Protocol-Version",
			Want:    ModernVersions[0],
		})
		if r.Header.Get("Mcp-Session-Id") != "" {
			t.Error("modern request carried a session id")
		}
		switch req.Method {
		case "server/discover":
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: DiscoverResult{SupportedVersions: ModernVersions},
			})
		case "tools/list":
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: ToolsListResult{Tools: []ToolDefinition{tool}},
			})
		case "tools/call":
			gotParams = slices.Clone(req.Params)
			call := callCount.Add(1)
			if call == 1 {
				assertModernHeader(t, modernHeaderParams{
					Request: r,
					Name:    "Mcp-Name",
					Want:    "tool name",
				})
				assertModernHeader(t, modernHeaderParams{
					Request: r,
					Name:    "Mcp-Param-Owner",
					Want:    "us-west1",
				})
				assertModernHeader(t, modernHeaderParams{
					Request: r,
					Name:    "Mcp-Param-Repo",
					Want:    "=?base64?SGVsbG8sIOS4lueVjA==?=",
				})
				assertModernHeader(t, modernHeaderParams{
					Request: r,
					Name:    "Mcp-Param-Enabled",
					Want:    "false",
				})
				assertModernHeader(t, modernHeaderParams{
					Request: r,
					Name:    "Mcp-Param-Nested",
					Want:    "42",
				})
			} else {
				for _, key := range []string{"Mcp-Param-Owner", "Mcp-Param-Repo", "Mcp-Param-Enabled", "Mcp-Param-Nested"} {
					assertModernHeader(t, modernHeaderParams{
						Request: r,
						Name:    key,
						Want:    "",
					})
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
	params := json.RawMessage(`{
		"name":"tool name",
		"arguments":{"owner":"us-west1","repo":"Hello, 世界","enabled":false,"a":{"b":42}},
		"custom":{"raw": [ 1, 2 ]},
		"_meta":{"io.modelcontextprotocol/clientCapabilities":{"custom":true}}
	}`)
	if _, err := conn.Call(t.Context(), "tools/call", params); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(methods, []string{"server/discover", "tools/list", "tools/call"}) {
		t.Fatalf("methods = %v", methods)
	}
	for _, want := range []string{`"custom":{"raw":[1,2]}`, `"io.modelcontextprotocol/clientCapabilities":{"custom":true}`, `"io.modelcontextprotocol/protocolVersion":"2026-07-28"`, `"io.modelcontextprotocol/clientInfo":{"name":"mini"`} {
		if !strings.Contains(string(gotParams), want) {
			t.Errorf("modern params %s missing %q", gotParams, want)
		}
	}
	missingArgs := json.RawMessage(`{"name":"tool name","arguments":{}}`)
	if _, err := conn.Call(t.Context(), "tools/call", missingArgs); err != nil {
		t.Fatal(err)
	}
}

func TestModernValueEncoding(t *testing.T) {
	cases := []struct{ input, want string }{
		{"us-west1", "us-west1"},
		{"Hello, 世界", "=?base64?SGVsbG8sIOS4lueVjA==?="},
		{" padded ", "=?base64?IHBhZGRlZCA=?="},
		{"line1\nline2", "=?base64?bGluZTEKbGluZTI=?="},
		{"=?base64?literal?=", "=?base64?PT9iYXNlNjQ/bGl0ZXJhbD89?="},
	}
	for _, tc := range cases {
		if got := encodeModernHeaderValue(tc.input); got != tc.want {
			t.Errorf("encodeModernHeaderValue(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestModernArgumentHeaderValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "integer", raw: `42`, want: "42"},
		{name: "integer decimal", raw: `42.0`, want: "42"},
		{name: "boolean", raw: `false`, want: "false"},
		{name: "unsafe integer", raw: `9007199254740992`},
		{name: "fraction", raw: `1.5`},
		{name: "null", raw: `null`},
		{name: "object", raw: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := primitiveHeaderValue(json.RawMessage(tc.raw))
			if ok != (tc.want != "") || got != tc.want {
				t.Fatalf("primitiveHeaderValue(%s) = %q, %v", tc.raw, got, ok)
			}
		})
	}
}

func modernTool(name, schema string) ToolDefinition {
	return ToolDefinition{Name: name, InputSchema: json.RawMessage(schema)}
}

// modernUpstream is a fake 2026-07-28 server: it answers server/discover itself, tools/list when
// Tools is set, and hands every other request to Handle (or answers {"ok":true}). Like a real
// modern server it has no GET stream, so a GET gets 405 and is counted in GetStreams.
type modernUpstream struct {
	Tools       []ToolDefinition
	ListChanged bool
	Probes      *atomic.Int32
	GetStreams  *atomic.Int32
	Handle      upstreamHandler
}

type upstreamHandler = func(http.ResponseWriter, *http.Request, Request)

func newModernServer(t *testing.T, u modernUpstream) *httptest.Server {
	t.Helper()
	return newJsonRPCServer(t, modernHandler(t, u))
}

func modernHandler(t *testing.T, u modernUpstream) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if u.GetStreams != nil {
				u.GetStreams.Add(1)
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		req := decodeRequest(t, r)
		switch {
		case req.Method == "server/discover":
			if u.Probes != nil {
				u.Probes.Add(1)
			}
			result := DiscoverResult{SupportedVersions: ModernVersions}
			if u.ListChanged {
				result.Capabilities.Tools = &ToolsCapability{ListChanged: true}
			}
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: result,
			})
		case req.Method == "tools/list" && u.Tools != nil:
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: ToolsListResult{Tools: u.Tools},
			})
		case u.Handle != nil:
			u.Handle(w, r, req)
		default:
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: map[string]bool{"ok": true},
			})
		}
	}
}

type modernHeaderParams struct {
	Request *http.Request
	Name    string
	Want    string
}

func assertModernHeader(t *testing.T, p modernHeaderParams) {
	t.Helper()
	if got := p.Request.Header.Get(p.Name); got != p.Want {
		t.Errorf("%s = %q, want %q", p.Name, got, p.Want)
	}
}

type httpRPCErrorParams struct {
	id   any
	code int
}

func writeHTTPRPCError(t *testing.T, w http.ResponseWriter, params httpRPCErrorParams) {
	t.Helper()
	w.WriteHeader(http.StatusBadRequest)
	_, err := fmt.Fprintf(w,
		`{"jsonrpc":"2.0","id":%v,"error":{"code":%d,"message":"error"}}`,
		params.id, params.code)
	if err != nil {
		t.Errorf("write RPC error: %v", err)
	}
}

func TestModernArgumentAtPathKeepsLevelsSeparate(t *testing.T) {
	for _, tc := range []struct {
		name string
		args string
		path []string
		want string
	}{
		{name: "missing nested value ignores parent key", args: `{"region":"outer","scope":{}}`, path: []string{"scope", "region"}},
		{name: "nested value", args: `{"region":"outer","scope":{"region":"inner"}}`, path: []string{"scope", "region"}, want: "inner"},
		{name: "root value beside a nested one", args: `{"region":"outer","scope":{"region":"inner"}}`, path: []string{"region"}, want: "outer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var args map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.args), &args); err != nil {
				t.Fatal(err)
			}
			got, ok := argumentAtPath(args, tc.path)
			if ok != (tc.want != "") || got != tc.want {
				t.Fatalf("argumentAtPath(%v) = %q, %v; want %q", tc.path, got, ok, tc.want)
			}
		})
	}
}

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
	list, err := conn.ListTools(t.Context())
	if err != nil || len(list.Tools) != 1 || list.Tools[0].Name != "x" {
		t.Fatalf("legacy ListTools = %v, %v", list.Tools, err)
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
	if list, err := conn.ListTools(t.Context()); err != nil || len(list.Tools) != 1 {
		t.Fatalf("ListTools = %v, %v; want the first page's tool", list.Tools, err)
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
