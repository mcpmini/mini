//go:build test

package transport

import (
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
		assertModernHeader(t, r, "Mcp-Method", req.Method)
		assertModernHeader(t, r, "MCP-Protocol-Version", ModernVersions[0])
		if r.Header.Get("Mcp-Session-Id") != "" {
			t.Error("modern request carried a session id")
		}
		switch req.Method {
		case "server/discover":
			writeRPCResult(t, w, req.ID, DiscoverResult{SupportedVersions: ModernVersions})
		case "tools/list":
			writeRPCResult(t, w, req.ID, ToolsListResult{Tools: []ToolDefinition{tool}})
		case "tools/call":
			gotParams = slices.Clone(req.Params)
			call := callCount.Add(1)
			if call == 1 {
				assertModernHeader(t, r, "Mcp-Name", "tool name")
				assertModernHeader(t, r, "Mcp-Param-Owner", "us-west1")
				assertModernHeader(t, r, "Mcp-Param-Repo", "=?base64?SGVsbG8sIOS4lueVjA==?=")
				assertModernHeader(t, r, "Mcp-Param-Enabled", "false")
				assertModernHeader(t, r, "Mcp-Param-Nested", "42")
			} else {
				for _, key := range []string{"Mcp-Param-Owner", "Mcp-Param-Repo", "Mcp-Param-Enabled", "Mcp-Param-Nested"} {
					assertModernHeader(t, r, key, "")
				}
			}
			writeRPCResult(t, w, req.ID, map[string]bool{"ok": true})
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

func TestModernRequestRefreshesAuthorizationAndReplays(t *testing.T) {
	var callAuth []string
	conn, provider := newAuthReplayConn(t, modernHandler(t, modernUpstream{
		Tools: []ToolDefinition{modernTool("x", `{}`)},
		Handle: func(w http.ResponseWriter, r *http.Request, req Request) {
			callAuth = append(callAuth, r.Header.Get("Authorization"))
			assertModernHeader(t, r, "Mcp-Method", "tools/call")
			if r.Header.Get("Authorization") == "Bearer old" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeRPCResult(t, w, req.ID, map[string]bool{"ok": true})
		},
	}))
	if _, err := conn.Call(t.Context(), "tools/call", json.RawMessage(`{"name":"x","arguments":{}}`)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(callAuth, []string{"Bearer old", "Bearer new"}) || provider.refreshes != 1 {
		t.Fatalf(
			"tools/call auth = %v, refreshes = %d; want old then new after one refresh",
			callAuth,
			provider.refreshes,
		)
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

func TestModernRequestRejectsNonObjectParams(t *testing.T) {
	conn := &HTTPConnection{}
	conn.modernVersion.Store(&ModernVersions[0])
	if _, err := conn.prepareModernParams(json.RawMessage(`[]`), ModernVersions[0]); err == nil ||
		err.Error() != "modern request params must be a JSON object" {
		t.Fatalf("prepareModernParams error = %v", err)
	}
}

func TestResultTypeOf(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want ResultType
		bad  bool
	}{
		{name: "absent", raw: `{}`, want: ResultComplete},
		{name: "complete", raw: `{"resultType":"complete"}`, want: ResultComplete},
		{name: "input required", raw: `{"resultType":"input_required"}`, want: ResultInputRequired},
		{name: "unknown", raw: `{"resultType":"bogus"}`, bad: true},
		{name: "non-string", raw: `{"resultType":42}`, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResultTypeOf(json.RawMessage(tc.raw))
			if tc.bad {
				if err == nil {
					t.Fatal("ResultTypeOf succeeded for invalid value")
				}
				if tc.name == "unknown" && err.Error() != `upstream returned unknown resultType "bogus"` {
					t.Fatalf("ResultTypeOf error = %v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("ResultTypeOf = %q, %v; want %q", got, err, tc.want)
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
	Handle      func(w http.ResponseWriter, r *http.Request, req Request)
}

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
			writeRPCResult(t, w, req.ID, result)
		case req.Method == "tools/list" && u.Tools != nil:
			writeRPCResult(t, w, req.ID, ToolsListResult{Tools: u.Tools})
		case u.Handle != nil:
			u.Handle(w, r, req)
		default:
			writeRPCResult(t, w, req.ID, map[string]bool{"ok": true})
		}
	}
}

func assertModernHeader(t *testing.T, r *http.Request, name, want string) {
	t.Helper()
	if got := r.Header.Get(name); got != want {
		t.Errorf("%s = %q, want %q", name, got, want)
	}
}

func writeHeaderMismatch(t *testing.T, w http.ResponseWriter, id any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, err := fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%v,"error":{"code":-32020,"message":"header mismatch"}}`, id)
	if err != nil {
		t.Errorf("write mismatch response: %v", err)
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
