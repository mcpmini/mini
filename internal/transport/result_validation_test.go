//go:build test

package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestStdioModernCallValidatesResultTypeAndPreservesBytes(t *testing.T) {
	for _, tc := range modernCallResultCases() {
		t.Run(tc.name, func(t *testing.T) {
			conn, serverW, serverR := makePipeConn(t)
			conn.setModern(ModernVersions[0])
			scriptStdioUpstream(serverW, serverR, replyRawResult(t, serverW, tc.raw))
			got, err := conn.Call(context.Background(), "ping", nil)
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
			got, err := conn.Call(t.Context(), "ping", nil)
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
		{name: "input required", raw: `{"resultType":"input_required","value":1}`},
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

func TestToolsListRequiresCompleteResultInBothTransports(t *testing.T) {
	for _, tc := range []resultTypeCase{
		{name: "missing", raw: `{"tools":[{"name":"x","inputSchema":{}}]}`},
		{name: "complete", raw: `{"resultType":"complete","tools":[{"name":"x","inputSchema":{}}]}`},
		{name: "input required", raw: `{"resultType":"input_required","tools":[{"name":"hidden","inputSchema":{}}]}`, wantErr: true},
		{name: "unknown", raw: `{"resultType":"future","tools":[{"name":"hidden","inputSchema":{}}]}`, wantErr: true},
		{name: "null", raw: `{"resultType":null,"tools":[]}`, wantErr: true},
		{name: "non-string", raw: `{"resultType":42,"tools":[]}`, wantErr: true},
	} {
		for _, modernHTTP := range []bool{false, true} {
			transportName := "stdio"
			if modernHTTP {
				transportName = "http"
			}
			t.Run(tc.name+"/"+transportName, func(t *testing.T) {
				testToolsListResult(t, tc, modernHTTP)
			})
		}
	}
}

func testToolsListResult(t *testing.T, tc resultTypeCase, modernHTTP bool) {
	t.Helper()
	if modernHTTP {
		server := newModernServer(t, modernUpstream{Handle: func(w http.ResponseWriter, _ *http.Request, req Request) {
			writeRawRPCResult(t, rawRPCResultParams{writer: w, id: req.ID, result: tc.raw})
		}})
		conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
		got, err := conn.ListTools(t.Context())
		assertToolsListResult(t, toolsListResultParams{testCase: tc, tools: got, err: err})
		return
	}
	conn, serverW, serverR := makePipeConn(t)
	conn.setModern(ModernVersions[0])
	scriptStdioUpstream(serverW, serverR, replyRawResult(t, serverW, tc.raw))
	got, err := conn.ListTools(context.Background())
	assertToolsListResult(t, toolsListResultParams{testCase: tc, tools: got, err: err})
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

func TestStdioDiscoveryResultTypes(t *testing.T) {
	for _, tc := range discoveryResultCases() {
		for _, correctedRetry := range []bool{false, true} {
			phase := "initial"
			if correctedRetry {
				phase = "corrected retry"
			}
			t.Run(tc.name+"/"+phase, func(t *testing.T) {
				testStdioDiscoveryResult(t, stdioDiscoveryResultParams{testCase: tc, correctedRetry: correctedRetry})
			})
		}
	}
}

type discoveryResultCase struct {
	name           string
	resultTypeJSON string
	wantErr        string
}

func discoveryResultCases() []discoveryResultCase {
	return []discoveryResultCase{
		{name: "missing"},
		{name: "complete", resultTypeJSON: `"complete"`},
		{name: "input required", resultTypeJSON: `"input_required"`, wantErr: "which mini cannot relay"},
		{name: "unknown", resultTypeJSON: `"future"`, wantErr: "unknown resultType"},
		{name: "null", resultTypeJSON: "null", wantErr: "unknown resultType"},
		{name: "non-string", resultTypeJSON: "42", wantErr: "unknown resultType"},
	}
}

type stdioDiscoveryResultParams struct {
	testCase       discoveryResultCase
	correctedRetry bool
}

func testStdioDiscoveryResult(t *testing.T, p stdioDiscoveryResultParams) {
	t.Helper()
	conn, serverW, serverR := makePipeConn(t)
	seen := scriptStdioUpstream(serverW, serverR, discoveryReplies(t, serverW, p)...)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := conn.negotiate(ctx)
	assertStdioDiscoveryOutcome(t, stdioDiscoveryOutcomeParams{testCase: p.testCase, conn: conn, err: err})
	wantMethods := expectedDiscoveryMethods(p.correctedRetry)
	if got := receiveMethods(seen, len(wantMethods)); !slices.Equal(got, wantMethods) {
		t.Fatalf("discovery requests = %v", got)
	}
}

func discoveryReplies(t *testing.T, w io.Writer, p stdioDiscoveryResultParams) []func(Request) {
	t.Helper()
	result := rawDiscoveryResult(p.testCase.resultTypeJSON)
	if !p.correctedRetry {
		return []func(Request){replyRawResult(t, w, result)}
	}
	supported := json.RawMessage(fmt.Sprintf(`{"requested":%q,"supported":[%q]}`, ModernVersions[0], ModernVersions[0]))
	retry := replyError(w, CodeUnsupportedProtocolVersion, supported)
	return []func(Request){retry, replyRawResult(t, w, result)}
}

type stdioDiscoveryOutcomeParams struct {
	testCase discoveryResultCase
	conn     *StdioConnection
	err      error
}

func assertStdioDiscoveryOutcome(t *testing.T, p stdioDiscoveryOutcomeParams) {
	t.Helper()
	if p.testCase.wantErr == "" {
		if p.err != nil || !p.conn.isModern() {
			t.Fatalf("negotiate = %v, modern=%v; want modern discovery", p.err, p.conn.isModern())
		}
		return
	}
	if p.err == nil || !strings.Contains(p.err.Error(), p.testCase.wantErr) {
		t.Fatalf("negotiate error = %v, want %s", p.err, p.testCase.wantErr)
	}
}

func expectedDiscoveryMethods(correctedRetry bool) []string {
	if correctedRetry {
		return []string{"server/discover", "server/discover"}
	}
	return []string{"server/discover"}
}

func TestHTTPDiscoveryResultTypes(t *testing.T) {
	for _, tc := range discoveryResultCases() {
		t.Run(tc.name, func(t *testing.T) { testHTTPDiscoveryResult(t, tc) })
	}
}

func testHTTPDiscoveryResult(t *testing.T, tc discoveryResultCase) {
	t.Helper()
	var methods []string
	server := discoveryHTTPServer(t, tc, &methods)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: server.URL})
	_, err := conn.Call(t.Context(), "ping", nil)
	assertHTTPDiscoveryOutcome(t, httpDiscoveryOutcomeParams{testCase: tc, methods: methods, err: err})
}

type httpDiscoveryOutcomeParams struct {
	testCase discoveryResultCase
	methods  []string
	err      error
}

func assertHTTPDiscoveryOutcome(t *testing.T, p httpDiscoveryOutcomeParams) {
	t.Helper()
	wantMethods := []string{"server/discover"}
	if p.testCase.wantErr == "" {
		wantMethods = append(wantMethods, "ping")
		if p.err != nil {
			t.Fatalf("Call after discovery = %v", p.err)
		}
	} else if p.err == nil || !strings.Contains(p.err.Error(), p.testCase.wantErr) {
		t.Fatalf("discovery error = %v, want %s", p.err, p.testCase.wantErr)
	}
	if !slices.Equal(p.methods, wantMethods) {
		t.Fatalf("methods = %v, want %v", p.methods, wantMethods)
	}
}

func discoveryHTTPServer(t *testing.T, tc discoveryResultCase, methods *[]string) *httptest.Server {
	t.Helper()
	return newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		*methods = append(*methods, req.Method)
		result := rawDiscoveryResult(tc.resultTypeJSON)
		if req.Method != "server/discover" {
			result = `{}`
		}
		writeRawRPCResult(t, rawRPCResultParams{writer: w, id: req.ID, result: result})
	})
}

func rawDiscoveryResult(resultTypeJSON string) string {
	result := fmt.Sprintf(`{"supportedVersions":[%q]}`, ModernVersions[0])
	if resultTypeJSON == "" {
		return result
	}
	return strings.Replace(result, "}", `,"resultType":`+resultTypeJSON+`}`, 1)
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

func TestPaginationReturnsOnlyVerifiedEarlierPages(t *testing.T) {
	page := 0
	first := json.RawMessage(`{"tools":[{"name":"verified","inputSchema":{}}],"nextCursor":"next"}`)
	second := json.RawMessage(`{"resultType":"future","tools":[{"name":"invalid","inputSchema":{}}]}`)
	got, err := paginateToolsList(context.Background(), func(context.Context, string) (ToolsListResult, error) {
		page++
		if page == 1 {
			return decodeToolsListPage(first)
		}
		return decodeToolsListPage(second)
	})
	if err != nil || !slices.Equal(toolNames(got), []string{"verified"}) {
		t.Fatalf("pagination = %v, %v; want only verified first page", toolNames(got), err)
	}
}
