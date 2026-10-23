//go:build test

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestClassifyDiscoverPassesTransportFailuresThrough(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "connection", err: &ConnectionError{Err: errors.New("dropped")}},
		{name: "reauthorization", err: ReauthorizationError("server", ErrReauthRequired)},
		{name: "retry exhaustion", err: errors.New("exceeded max retries")},
		{name: "context", err: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := classifyDiscover(nil, tc.err); !errors.Is(err, tc.err) {
				t.Fatalf("error = %v, want unchanged input error %v", err, tc.err)
			}
		})
	}
}

type discoverVerdictWant struct {
	era    Era
	listen bool
	err    string
}

type discoverClassifierCase struct {
	name      string
	result    string
	rejection *RPCError
	want      discoverVerdictWant
	stdioWant *discoverVerdictWant
}

func TestDiscoverClassifiersByTransportPolicy(t *testing.T) {
	modern := discoverVerdictWant{era: EraModern}
	legacy := discoverVerdictWant{era: EraLegacy}
	refused := func(supported string) *discoverVerdictWant {
		message := `upstream does not support requested modern protocol "2026-07-28" (supported: ` + supported + `)`
		return &discoverVerdictWant{err: message}
	}
	unsupported := func(data string) *RPCError {
		return &RPCError{Code: CodeUnsupportedProtocolVersion, Data: json.RawMessage(data)}
	}
	for _, tc := range []discoverClassifierCase{
		{name: "modern result with tool changes", result: `{"supportedVersions":["2026-07-28"],"capabilities":{"tools":{"listChanged":true}}}`, want: discoverVerdictWant{era: EraModern, listen: true}},
		{name: "modern result without tool changes", result: `{"supportedVersions":["2026-07-28"],"capabilities":{"tools":{"listChanged":false}}}`, want: modern},
		{name: "result listing only legacy versions", result: `{"supportedVersions":["2025-11-25"]}`, want: legacy, stdioWant: refused("[2025-11-25]")},
		{name: "result without supported versions", result: `{}`, want: legacy},
		{name: "malformed result", result: `not json`, want: legacy},
		{name: "result asking for input", result: `{"supportedVersions":["2026-07-28"],"resultType":"input_required"}`, want: discoverVerdictWant{err: "validate modern discovery result: upstream asked for input during server/discover, which mini cannot relay"}},
		{name: "result of unknown type", result: `{"supportedVersions":["2026-07-28"],"resultType":"future"}`, want: discoverVerdictWant{err: `validate modern discovery result: upstream returned unknown resultType "future"`}},
		{name: "method not found", rejection: &RPCError{Code: CodeMethodNotFound, Message: "Method not found"}, want: legacy},
		{name: "header mismatch", rejection: &RPCError{Code: CodeHeaderMismatch, Message: "header mismatch"}, want: discoverVerdictWant{err: "modern discovery rejected: header mismatch"}},
		{name: "missing client capability", rejection: &RPCError{Code: CodeMissingRequiredClientCapability, Message: "client mismatch"}, want: discoverVerdictWant{err: "modern discovery rejected: client mismatch"}},
		{name: "unsupported version naming a shared one", rejection: unsupported(`{"supported":["2026-07-28","2025-11-25"]}`), want: modern},
		{name: "unsupported version naming only legacy", rejection: unsupported(`{"supported":["2025-11-25"]}`), want: legacy, stdioWant: refused("[2025-11-25]")},
		{name: "unsupported version with undecodable data", rejection: unsupported(`"none"`), want: legacy, stdioWant: refused("[]")},
		{name: "unsupported version without data", rejection: unsupported(""), want: legacy, stdioWant: refused("[]")},
	} {
		stdioWant := tc.want
		if tc.stdioWant != nil {
			stdioWant = *tc.stdioWant
		}
		stdioPolicy := stdioDiscoverPolicy(ModernVersions[0])
		t.Run(
			tc.name+"/http",
			func(t *testing.T) { assertDiscoverVerdict(t, tc.classify(httpDiscoverPolicy), tc.want) },
		)
		t.Run(tc.name+"/stdio", func(t *testing.T) { assertDiscoverVerdict(t, tc.classify(stdioPolicy), stdioWant) })
	}
}

type discoverOutcome struct {
	verdict probeVerdict
	err     error
}

func (tc discoverClassifierCase) classify(policy discoverPolicy) discoverOutcome {
	if tc.rejection != nil {
		verdict, err := classifyDiscoverRejection(tc.rejection, policy)
		return discoverOutcome{verdict: verdict, err: err}
	}
	verdict, err := classifyDiscoverResult(json.RawMessage(tc.result), policy)
	return discoverOutcome{verdict: verdict, err: err}
}

func assertDiscoverVerdict(t *testing.T, got discoverOutcome, want discoverVerdictWant) {
	t.Helper()
	if want.err != "" {
		if got.err == nil || got.err.Error() != want.err {
			t.Fatalf("error = %v, want %q", got.err, want.err)
		}
		return
	}
	if got.err != nil {
		t.Fatalf("error = %v, want era %v", got.err, want.era)
	}
	wantVersion := ""
	if want.era == EraModern {
		wantVersion = ModernVersions[0]
	}
	if v := got.verdict; v.era != want.era || v.version != wantVersion || v.listen != want.listen {
		t.Fatalf("verdict = %+v, want era %v version %q listen %v", v, want.era, wantVersion, want.listen)
	}
}

func TestClassifyDiscoverResponses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		dropped bool
		era     Era
		wantErr bool
	}{
		{name: "modern result", status: 200, body: `{"jsonrpc":"2.0","id":1,"result":{"supportedVersions":["2026-07-28","2025-11-25"],"capabilities":{}}}`, era: EraModern},
		{name: "method not found", status: 200, body: `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`, era: EraLegacy},
		{name: "pretty-printed error body", status: 200, body: "{\n\t\"jsonrpc\":\"2.0\",\n\t\"id\":1,\n\t\"error\":{\"code\":-32601,\"message\":\"Unknown method\"}\n}", era: EraLegacy},
		{name: "405 with JSON-RPC body", status: 405, body: `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"not supported"}}`, era: EraLegacy},
		// Seen in the wild: legacy servers that reject the version answer 400 with an id that isn't the request's.
		{name: "400 with a non-request id", status: 400, body: `{"jsonrpc":"2.0","id":"server-error","error":{"code":-32600,"message":"Unsupported protocol version"}}`, era: EraLegacy},
		{name: "400 with a null id", status: 400, body: `{"jsonrpc":"2.0","id":null,"error":{"code":-32000,"message":"Unsupported protocol version"}}`, era: EraLegacy},
		{name: "unsupported version listing modern", status: 200, body: `{"jsonrpc":"2.0","id":1,"error":{"code":-32022,"message":"Unsupported protocol version","data":{"supported":["2026-07-28","2025-11-25"]}}}`, era: EraModern},
		{name: "unsupported version listing only legacy", status: 200, body: `{"jsonrpc":"2.0","id":1,"error":{"code":-32022,"message":"Unsupported protocol version","data":{"supported":["2025-11-25","2025-06-18"]}}}`, era: EraLegacy},
		{name: "header mismatch", status: 200, body: `{"jsonrpc":"2.0","id":1,"error":{"code":-32020,"message":"header mismatch"}}`, wantErr: true},
		{name: "client mismatch", status: 200, body: `{"jsonrpc":"2.0","id":1,"error":{"code":-32021,"message":"client mismatch"}}`, wantErr: true},
		{name: "forbidden", status: 403, body: "forbidden", wantErr: true},
		{name: "timeout", status: 408, body: "timeout", wantErr: true},
		{name: "rate limited", status: 429, body: "retry later", wantErr: true},
		{name: "bad gateway HTML", status: 502, body: "<html>gateway error</html>", wantErr: true},
		{name: "dropped connection", dropped: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := discoverFixtureServer(t, discoverFixture{status: tc.status, body: tc.body, dropped: tc.dropped})
			conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
			result, callErr := conn.rpc(t.Context(), "server/discover", nil)
			got, err := classifyDiscover(result, callErr)
			if (err != nil) != tc.wantErr {
				t.Fatalf("classifyDiscover error = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && got.era != tc.era {
				t.Errorf("era = %v, want %v", got.era, tc.era)
			}
			if got.era == EraModern && got.version != ModernVersions[0] {
				t.Errorf("modern version = %q, want %q", got.version, ModernVersions[0])
			}
		})
	}
}

type discoverFixture struct {
	status  int
	body    string
	dropped bool
}

func discoverFixtureServer(t *testing.T, f discoverFixture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.dropped {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack connection: %v", err)
				return
			}
			if err := conn.Close(); err != nil {
				t.Errorf("close connection: %v", err)
			}
			return
		}
		if f.status == http.StatusTooManyRequests || f.status == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "0")
		}
		w.WriteHeader(f.status)
		if _, err := fmt.Fprint(w, f.body); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
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
	replies := discoveryReplies(t, serverW, p)
	seen := scriptStdioUpstream(serverW, serverR, replies...)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err := conn.negotiate(ctx)
	if p.testCase.wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), p.testCase.wantErr) {
			t.Fatalf("negotiate error = %v, want %s", err, p.testCase.wantErr)
		}
	} else if err != nil || !conn.isModern() {
		t.Fatalf("negotiate = %v, modern=%v; want modern discovery", err, conn.isModern())
	}
	methods := receiveMethods(seen, len(replies))
	if len(methods) != len(replies) {
		t.Fatalf("discovery sent %v, want %d requests", methods, len(replies))
	}
	for _, method := range methods {
		if method != "server/discover" {
			t.Fatalf("discovery sent %s, want server/discover", method)
		}
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
	wantMethods := []string{"server/discover"}
	if tc.wantErr == "" {
		wantMethods = append(wantMethods, "ping")
		if err != nil {
			t.Fatalf("Call after discovery = %v", err)
		}
	} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
		t.Fatalf("discovery error = %v, want %s", err, tc.wantErr)
	}
	if !slices.Equal(methods, wantMethods) {
		t.Fatalf("methods = %v, want %v", methods, wantMethods)
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
