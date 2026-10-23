//go:build test

package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
)

func TestModernRequestRefreshesAuthorizationAndReplays(t *testing.T) {
	var callAuth []string
	conn, provider := newAuthReplayConn(t, modernHandler(t, modernUpstream{
		Tools: []ToolDefinition{modernTool("x", `{}`)},
		Handle: func(w http.ResponseWriter, r *http.Request, req Request) {
			callAuth = append(callAuth, r.Header.Get("Authorization"))
			assertModernHeader(t, modernHeaderParams{
				Request: r,
				Name:    "Mcp-Method",
				Want:    "tools/call",
			})
			if r.Header.Get("Authorization") == "Bearer old" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: map[string]bool{"ok": true},
			})
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

func TestModernHeaderMismatchRelistsAndRetriesOnce(t *testing.T) {
	var lists, calls atomic.Int32
	srv := newModernServer(t, modernUpstream{Handle: func(w http.ResponseWriter, r *http.Request, req Request) {
		switch req.Method {
		case "tools/list":
			name := "Old"
			if lists.Add(1) == 2 {
				name = "New"
			}
			tool := modernTool("x", `{"type":"object","properties":{"p":{"type":"string","x-mcp-header":"`+name+`"}}}`)
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: ToolsListResult{Tools: []ToolDefinition{tool}},
			})
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
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: map[string]bool{"ok": true},
			})
		}
	}})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
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
	srv := newModernServer(t, modernUpstream{
		Tools: []ToolDefinition{modernTool("x", `{"type":"object","properties":{}}`)},
		Handle: func(w http.ResponseWriter, _ *http.Request, req Request) {
			calls.Add(1)
			writeHeaderMismatch(t, w, req.ID)
		},
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	_, err := conn.Call(t.Context(), "tools/call", json.RawMessage(`{"name":"x","arguments":{}}`))
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32020 {
		t.Fatalf("error = %v, want -32020 RPCError", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func rpcErrorUntilReprobe(t *testing.T, code int) (*HTTPConnection, *atomic.Int32) {
	t.Helper()
	var probes atomic.Int32
	srv := newModernServer(
		t,
		modernUpstream{Probes: &probes, Handle: func(w http.ResponseWriter, _ *http.Request, req Request) {
			if probes.Load() == 1 {
				writeHTTPRPCError(t, w, httpRPCErrorParams{id: req.ID, code: code})
				return
			}
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: map[string]bool{"ok": true},
			})
		}},
	)
	return mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL}), &probes
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
	srv := newModernServer(
		t,
		modernUpstream{Probes: &probes, Handle: func(w http.ResponseWriter, _ *http.Request, req Request) {
			if probes.Load() == 1 {
				w.WriteHeader(http.StatusNotFound)
				if _, err := w.Write([]byte("<html>missing</html>")); err != nil {
					t.Errorf("write HTML error body: %v", err)
				}
				return
			}
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: map[string]bool{"ok": true},
			})
		}},
	)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	_, err := conn.Call(t.Context(), "ping", nil)
	if !IsConnectionError(err) {
		t.Fatalf("404 error = %T %v, want ConnectionError", err, err)
	}
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil || probes.Load() != 2 {
		t.Fatalf("call after 404 reset: %v, probes=%d", err, probes.Load())
	}
}

func TestModernCallKeepsItsEraThroughAConcurrentReset(t *testing.T) {
	upstream := startResettingUpstream(t)
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: upstream.url})
	called := callInBackground(t, backgroundCallParams{
		Connection: conn,
		Method:     "tools/call",
		Params:     `{"name":"x","arguments":{"owner":"octo"}}`,
	})
	<-upstream.listing
	if _, err := conn.Call(t.Context(), "ping", nil); err == nil {
		t.Fatal("ping succeeded, want the unsupported-version error that resets the era")
	}
	close(upstream.releaseList)
	if err := <-called; err != nil {
		t.Fatal(err)
	}
	headers := <-upstream.callHeaders
	if headers.Get("MCP-Protocol-Version") != ModernVersions[0] || headers.Get("Mcp-Method") != "tools/call" ||
		headers.Get("Mcp-Param-Owner") != "octo" {
		t.Fatalf("tools/call sent with protocol %q, method %q, owner %q after a concurrent reset, want modern headers",
			headers.Get("MCP-Protocol-Version"), headers.Get("Mcp-Method"), headers.Get("Mcp-Param-Owner"))
	}
}

type resettingUpstream struct {
	url         string
	listing     chan struct{}
	releaseList chan struct{}
	callHeaders chan http.Header
}

func startResettingUpstream(t *testing.T) *resettingUpstream {
	t.Helper()
	u := &resettingUpstream{
		listing: make(chan struct{}), releaseList: make(chan struct{}), callHeaders: make(chan http.Header, 1),
	}
	u.url = newModernServer(t, modernUpstream{Handle: u.handle(t)}).URL
	// Lets the server's Close finish when a failed test never released tools/list.
	t.Cleanup(func() {
		select {
		case <-u.releaseList:
		default:
			close(u.releaseList)
		}
	})
	return u
}

func (u *resettingUpstream) handle(t *testing.T) upstreamHandler {
	return func(w http.ResponseWriter, r *http.Request, req Request) {
		switch req.Method {
		case "tools/list":
			close(u.listing)
			<-u.releaseList
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: ToolsListResult{Tools: []ToolDefinition{modernTool("x", ownerHeaderSchema)}},
			})
		case "ping":
			writeHTTPRPCError(t, w, httpRPCErrorParams{id: req.ID, code: CodeUnsupportedProtocolVersion})
		case "tools/call":
			u.callHeaders <- r.Header.Clone()
			writeRPCResult(t, rpcResultParams{
				Writer: w,
				ID:     req.ID,
				Result: map[string]bool{"ok": true},
			})
		}
	}
}

type backgroundCallParams struct {
	Connection *HTTPConnection
	Method     string
	Params     string
}

func callInBackground(t *testing.T, p backgroundCallParams) <-chan error {
	t.Helper()
	called := make(chan error, 1)
	go func() {
		_, err := p.Connection.Call(t.Context(), p.Method, json.RawMessage(p.Params))
		called <- err
	}()
	return called
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

func TestIsHeaderMismatch(t *testing.T) {
	mismatch := &RPCError{Code: CodeHeaderMismatch}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"rpc error", mismatch, true},
		{"wrapped rpc error", fmt.Errorf("call: %w", mismatch), true},
		{"http status carrying the rpc error", &HTTPStatusError{StatusCode: 400, RPC: mismatch}, true},
		{"http status without rpc error", &HTTPStatusError{StatusCode: 400}, false},
		{"other rpc error", &RPCError{Code: CodeInvalidParams}, false},
		{"plain error", errors.New("boom"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHeaderMismatch(tc.err); got != tc.want {
				t.Fatalf("isHeaderMismatch(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestModernServerErrorKeepsTheEra(t *testing.T) {
	conn := &HTTPConnection{}
	version := ModernVersions[0]
	conn.modernVersion.Store(&version)
	failure := &HTTPStatusError{StatusCode: http.StatusBadGateway}
	err := conn.modernError(failure)
	if !errors.Is(err, failure) || IsConnectionError(err) || !conn.isModern() {
		t.Fatalf("5xx became %v (connection error: %v), modern = %v; want it returned as is with the era kept",
			err, IsConnectionError(err), conn.isModern())
	}
}
