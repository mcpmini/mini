//go:build test

package transport

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

//go:embed testdata/discover/*.json
var discoverFixtures embed.FS

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

func TestClassifyDiscoverFixtures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		dropped bool
		era     Era
		wantErr bool
	}{
		{name: "huggingface", status: 200, body: "huggingface.json", era: EraModern},
		{name: "cloudflare-docs", status: 200, body: "cloudflare-docs.json", era: EraModern},
		{name: "aws-knowledge", status: 200, body: "aws-knowledge.json", era: EraLegacy},
		{name: "wolfram", status: 200, body: "wolfram.json", era: EraLegacy},
		{name: "twilio-docs", status: 405, body: "twilio-docs.json", era: EraLegacy},
		{name: "deepwiki", status: 400, body: "deepwiki.json", era: EraLegacy},
		{name: "exa", status: 400, body: "exa.json", era: EraLegacy},
		{name: "github-like", status: 200, body: "github-like.json", era: EraModern},
		{name: "unsupported modern", status: 200, body: "unsupported-modern.json", era: EraModern},
		{name: "unsupported legacy", status: 200, body: "unsupported-legacy-only.json", era: EraLegacy},
		{name: "header mismatch", status: 200, body: "header-mismatch.json", wantErr: true},
		{name: "client mismatch", status: 200, body: `{"jsonrpc":"2.0","id":7,"error":{"code":-32021,"message":"client mismatch"}}`, wantErr: true},
		{name: "forbidden", status: 403, body: "forbidden", wantErr: true},
		{name: "timeout", status: 408, body: "timeout", wantErr: true},
		{name: "rate limited", status: 429, body: "retry later", wantErr: true},
		{name: "bad gateway HTML", status: 502, body: "<html>gateway error</html>", wantErr: true},
		{name: "dropped connection", dropped: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			if strings.HasSuffix(body, ".json") {
				contents, err := discoverFixtures.ReadFile("testdata/discover/" + body)
				if err != nil {
					t.Fatal(err)
				}
				body = string(contents)
			}
			srv := discoverFixtureServer(t, discoverFixture{status: tc.status, body: body, dropped: tc.dropped})
			conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
			conn.nextID.Store(6)
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
