//go:build test

package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestStdioModernCallValidatesResultTypeAndPreservesBytes(t *testing.T) {
	for _, tc := range modernCallResultCases() {
		t.Run(tc.name, func(t *testing.T) {
			conn, serverW, serverR := makePipeConn(t)
			conn.setModern(ModernVersions[0])
			scriptStdioUpstream(serverW, serverR, replyRawResult(t, serverW, tc.raw))
			got, err := conn.Call(context.Background(), "resources/read", json.RawMessage(`{"uri":"test://document"}`))
			assertModernCallResult(t, modernCallResultParams{testCase: tc, result: got, err: err})
		})
	}
}

func TestHTTPModernCallValidatesResultTypeAndPreservesBytes(t *testing.T) {
	for _, tc := range modernCallResultCases() {
		t.Run(tc.name, func(t *testing.T) {
			server := newModernServer(t, modernUpstream{
				Handle: func(w http.ResponseWriter, _ *http.Request, req Request) {
					writeRawRPCResult(t, rawRPCResultParams{writer: w, id: req.ID, result: tc.raw})
				},
			})
			conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
			got, err := conn.Call(t.Context(), "resources/read", json.RawMessage(`{"uri":"test://document"}`))
			assertModernCallResult(t, modernCallResultParams{testCase: tc, result: got, err: err})
		})
	}
}

func TestLegacyCallsKeepGenericResultBehavior(t *testing.T) {
	const result = `{"resultType":"future","value":1}`
	t.Run("stdio", func(t *testing.T) {
		conn, serverW, serverR := makePipeConn(t)
		scriptStdioUpstream(serverW, serverR, replyRawResult(t, serverW, result))
		got, err := conn.Call(context.Background(), "ping", nil)
		if err != nil || string(got) != result {
			t.Fatalf("Call = %s, %v; want original generic result", got, err)
		}
	})
	t.Run("http", func(t *testing.T) {
		server := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
			req := decodeRequest(t, r)
			switch req.Method {
			case "initialize":
				writeRPCResult(t, rpcResultParams{Writer: w, ID: req.ID, Result: InitializeResult{
					ProtocolVersion: LegacyVersions[0], ServerInfo: ServerInfo{Name: "fake", Version: "1"},
				}})
			case NotificationInitialized:
				w.WriteHeader(http.StatusAccepted)
			default:
				writeRawRPCResult(t, rawRPCResultParams{writer: w, id: req.ID, result: result})
			}
		})
		conn := mustLegacyHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
		got, err := conn.Call(t.Context(), "ping", nil)
		if err != nil || string(got) != result {
			t.Fatalf("Call = %s, %v; want original generic result", got, err)
		}
	})
}

func modernCallResultCases() []resultTypeCase {
	return []resultTypeCase{
		{name: "missing", raw: `{ "value" : 1 }`},
		{name: "complete", raw: `{ "resultType" : "complete", "value" : 1 }`},
		{name: "input required", raw: `{"resultType":"input_required","requestState":"opaque"}`},
		{name: "unknown", raw: `{"resultType":"future","value":1}`, wantErr: true},
		{name: "null", raw: `{"resultType":null}`, wantErr: true},
		{name: "non-string", raw: `{"resultType":42}`, wantErr: true},
	}
}

type resultTypeCase struct {
	name    string
	raw     string
	wantErr bool
}

type modernCallResultParams struct {
	testCase resultTypeCase
	result   json.RawMessage
	err      error
}

func assertModernCallResult(t *testing.T, p modernCallResultParams) {
	t.Helper()
	if p.testCase.wantErr {
		if p.err == nil || !strings.Contains(p.err.Error(), "unknown resultType") {
			t.Fatalf("Call error = %v, want unknown resultType", p.err)
		}
		return
	}
	if p.err != nil || string(p.result) != p.testCase.raw {
		t.Fatalf("Call = %s, %v; want original bytes %s", p.result, p.err, p.testCase.raw)
	}
}

func TestStdioToolsListRequiresCompleteResult(t *testing.T) {
	for _, tc := range toolsListResultCases() {
		t.Run(tc.name, func(t *testing.T) {
			conn, serverW, serverR := makePipeConn(t)
			conn.setModern(ModernVersions[0])
			scriptStdioUpstream(serverW, serverR, replyRawResult(t, serverW, tc.raw))
			got, err := conn.ListTools(t.Context())
			assertToolsListResult(t, toolsListResultParams{testCase: tc, tools: got.Tools, err: err})
		})
	}
}

func TestHTTPToolsListRequiresCompleteResult(t *testing.T) {
	for _, tc := range toolsListResultCases() {
		t.Run(tc.name, func(t *testing.T) {
			server := newModernServer(t, modernUpstream{
				Handle: func(w http.ResponseWriter, _ *http.Request, req Request) {
					writeRawRPCResult(t, rawRPCResultParams{writer: w, id: req.ID, result: tc.raw})
				},
			})
			conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
			got, err := conn.ListTools(t.Context())
			assertToolsListResult(t, toolsListResultParams{testCase: tc, tools: got.Tools, err: err})
		})
	}
}

func toolsListResultCases() []resultTypeCase {
	return []resultTypeCase{
		{name: "missing", raw: `{"tools":[{"name":"x","inputSchema":{}}]}`},
		{name: "complete", raw: `{"resultType":"complete","tools":[{"name":"x","inputSchema":{}}]}`},
		{
			name:    "input required",
			raw:     `{"resultType":"input_required","tools":[{"name":"hidden","inputSchema":{}}]}`,
			wantErr: true,
		},
		{name: "unknown", raw: `{"resultType":"future","tools":[{"name":"hidden","inputSchema":{}}]}`, wantErr: true},
		{name: "null", raw: `{"resultType":null,"tools":[]}`, wantErr: true},
		{name: "non-string", raw: `{"resultType":42,"tools":[]}`, wantErr: true},
	}
}

type toolsListResultParams struct {
	testCase resultTypeCase
	tools    []ToolDefinition
	err      error
}

func assertToolsListResult(t *testing.T, p toolsListResultParams) {
	t.Helper()
	if p.testCase.wantErr {
		if p.err == nil || (!strings.Contains(p.err.Error(), "unknown resultType") &&
			!strings.Contains(p.err.Error(), "which mini cannot relay")) {
			t.Fatalf("ListTools = %v, %v; want unusable result type error", p.tools, p.err)
		}
		if len(p.tools) != 0 {
			t.Fatalf("ListTools exposed tools from invalid page: %v", toolNames(p.tools))
		}
		return
	}
	if p.err != nil || !slices.Equal(toolNames(p.tools), []string{"x"}) {
		t.Fatalf("ListTools = %v, %v; want tool x", toolNames(p.tools), p.err)
	}
}

func replyRawResult(t *testing.T, w io.Writer, result string) func(Request) {
	t.Helper()
	return func(req Request) {
		writeRawRPCResult(t, rawRPCResultParams{writer: w, id: req.ID, result: result})
	}
}

type rawRPCResultParams struct {
	writer io.Writer
	id     any
	result string
}

func writeRawRPCResult(t *testing.T, p rawRPCResultParams) {
	t.Helper()
	idBytes, err := json.Marshal(p.id)
	if err != nil {
		t.Fatalf("marshal response id: %v", err)
	}
	if _, err := fmt.Fprintf(p.writer, `{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", idBytes, p.result); err != nil {
		t.Fatalf("write raw result: %v", err)
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
		{name: "null", raw: `{"resultType":null}`, bad: true},
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
