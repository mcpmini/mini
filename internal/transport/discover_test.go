//go:build test

package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClassifyDiscover(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  string
		err     error
		wantEra Era
		wantVer string
		wantErr bool
	}{
		{name: "modern result", result: `{"supportedVersions":["2026-07-28"]}`, wantEra: EraModern, wantVer: "2026-07-28"},
		{name: "legacy result", result: `{"supportedVersions":["2025-11-25"]}`, wantEra: EraLegacy},
		{name: "malformed result", result: `not json`, wantEra: EraLegacy},
		{name: "unsupported modern", err: unsupportedRPC(-32022, `{"supported":["2026-07-28"]}`), wantEra: EraModern, wantVer: "2026-07-28"},
		{name: "unsupported legacy", err: unsupportedRPC(-32022, `{"supported":["2025-11-25"]}`), wantEra: EraLegacy},
		{name: "unsupported without data", err: &RPCError{Code: -32022}, wantEra: EraLegacy},
		{name: "method missing", err: &RPCError{Code: -32601}, wantEra: EraLegacy},
		{name: "header mismatch", err: &RPCError{Code: -32020}, wantErr: true},
		{name: "client mismatch", err: &RPCError{Code: -32021}, wantErr: true},
		{name: "unsupported status", err: &HTTPStatusError{StatusCode: 405}, wantEra: EraLegacy},
		{name: "bad request status", err: &HTTPStatusError{StatusCode: 400}, wantEra: EraLegacy},
		{name: "forbidden status", err: &HTTPStatusError{StatusCode: 403}, wantErr: true},
		{name: "server status", err: &HTTPStatusError{StatusCode: 500}, wantErr: true},
		{name: "connection", err: &ConnectionError{Err: errors.New("dropped")}, wantErr: true},
		{name: "reauthorization", err: ReauthorizationError("server", ErrReauthRequired), wantErr: true},
		{name: "retry exhaustion", err: errors.New("exceeded max retries"), wantErr: true},
		{name: "context", err: context.Canceled, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := classifyDiscover(json.RawMessage(tc.result), tc.err)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error %v", err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, tc.err) {
				t.Errorf("error = %v, want unchanged input error %v", err, tc.err)
			}
			if got.era != tc.wantEra || got.version != tc.wantVer {
				t.Errorf("verdict = %+v, want era %v version %q", got, tc.wantEra, tc.wantVer)
			}
		})
	}
}

func unsupportedRPC(code int, data string) *RPCError {
	return &RPCError{Code: code, Data: json.RawMessage(data)}
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
