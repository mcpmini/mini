//go:build test

package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mcpmini/mini/internal/clock"
)

func TestHTTPConnection_ProbeRunsBeforeInitialize(t *testing.T) {
	var methods []string
	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		methods = append(methods, req.Method)
		if req.Method == "server/discover" {
			assertDiscoverRequest(t, r, req)
			writeRPCResult(t, w, req.ID, DiscoverResult{SupportedVersions: LegacyVersions})
			return
		}
		serveProbeCall(t, w, req)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	want := []string{"server/discover", "initialize", NotificationInitialized, "ping"}
	if !slices.Equal(methods, want) {
		t.Fatalf("methods = %v, want %v", methods, want)
	}
}

func TestHTTPConnection_ProbeWithoutAnAnswerFallsBackToLegacy(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "empty 200", status: http.StatusOK},
		{name: "empty 202", status: http.StatusAccepted},
		{name: "unmatched id", status: http.StatusOK, body: `{"jsonrpc":"2.0","id":99,"result":{}}`},
		{name: "not JSON", status: http.StatusOK, body: "ok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var methods []string
			srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
				req := decodeRequest(t, r)
				methods = append(methods, req.Method)
				if req.Method == "server/discover" {
					w.WriteHeader(tt.status)
					if _, err := fmt.Fprint(w, tt.body); err != nil {
						t.Errorf("write discover reply: %v", err)
					}
					return
				}
				serveProbeCall(t, w, req)
			})
			conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
			if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
				t.Fatalf("Call: %v", err)
			}
			want := []string{"server/discover", "initialize", NotificationInitialized, "ping"}
			if !slices.Equal(methods, want) || conn.isModern() {
				t.Fatalf("methods = %v, modern = %v; want %v and legacy", methods, conn.isModern(), want)
			}
		})
	}
}

func TestHTTPConnection_ProbeModernUsesModernRequests(t *testing.T) {
	var methods []string
	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		methods = append(methods, req.Method)
		if req.Method == "server/discover" {
			assertDiscoverRequest(t, r, req)
			writeRPCResult(
				t,
				w,
				req.ID,
				DiscoverResult{SupportedVersions: []string{ModernVersions[0], LatestLegacyVersion}},
			)
			return
		}
		serveProbeCall(t, w, req)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !slices.Equal(methods, []string{"server/discover", "ping"}) {
		t.Fatalf("methods = %v", methods)
	}
	if !conn.isModern() || conn.currentModernVersion() != ModernVersions[0] {
		t.Errorf("modern = %v, version = %q", conn.isModern(), conn.currentModernVersion())
	}
}

func TestHTTPConnection_failedProbeRetriesNextCall(t *testing.T) {
	var probes atomic.Int32
	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		if req.Method == "server/discover" && probes.Add(1) <= maxRetries {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := fmt.Fprint(w, "temporarily unavailable"); err != nil {
				t.Errorf("write unavailable response: %v", err)
			}
			return
		}
		if req.Method == "server/discover" {
			writeRPCResult(t, w, req.ID, DiscoverResult{})
			return
		}
		serveProbeCall(t, w, req)
	})
	clk := clock.NewFake()
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL, Clock: clk})
	first := make(chan error, 1)
	go func() {
		_, err := conn.Call(t.Context(), "ping", nil)
		first <- err
	}()
	advanceRetryTimer(t, clk)
	advanceRetryTimer(t, clk)
	if err := <-first; err == nil {
		t.Fatal("first call succeeded after exhausted probe retries")
	}
	if conn.eraKnown {
		t.Fatal("failed probe cached an era")
	}
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("second Call: %v", err)
	}
	if got := probes.Load(); got != maxRetries+1 {
		t.Errorf("probe attempts = %d, want %d", got, maxRetries+1)
	}
}

func TestHTTPConnection_ProbeRefreshesAuthorization(t *testing.T) {
	var probeCount atomic.Int32
	conn, provider := newAuthReplayConn(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		if req.Method == "server/discover" {
			probeCount.Add(1)
			if r.Header.Get("Authorization") == "Bearer old" {
				w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeRPCResult(t, w, req.ID, DiscoverResult{})
			return
		}
		serveProbeCall(t, w, req)
	})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if probeCount.Load() != 2 || provider.refreshCount() != 1 {
		t.Errorf("probe count/refresh count = %d/%d, want 2/1", probeCount.Load(), provider.refreshCount())
	}
}

func TestHTTPConnection_ProbeSessionRenewalDoesNotReprobe(t *testing.T) {
	var probes, calls atomic.Int32
	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		switch req.Method {
		case "server/discover":
			probes.Add(1)
			writeRPCResult(t, w, req.ID, DiscoverResult{})
		case "initialize":
			w.Header().Set("Mcp-Session-Id", fmt.Sprintf("s%d", calls.Load()+1))
			serveProbeCall(t, w, req)
		case NotificationInitialized:
			w.WriteHeader(http.StatusAccepted)
		case "ping":
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeRPCResult(t, w, req.ID, map[string]bool{"ok": true})
		}
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	if _, err := conn.Call(t.Context(), "ping", nil); err != nil {
		t.Fatalf("Call with renewal: %v", err)
	}
	if probes.Load() != 1 || calls.Load() != 2 {
		t.Errorf("probe count/ping count = %d/%d, want 1/2", probes.Load(), calls.Load())
	}
}

func TestHTTPConnection_concurrentFirstCallsProbeOnce(t *testing.T) {
	var probes atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	srv := newJsonRPCServer(t, func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		if req.Method == "server/discover" {
			probes.Add(1)
			close(entered)
			<-release
			writeRPCResult(t, w, req.ID, DiscoverResult{})
			return
		}
		serveProbeCall(t, w, req)
	})
	conn := mustHTTPConn(t, HTTPConnectionConfig{URL: srv.URL})
	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := conn.Call(context.Background(), "ping", nil)
			errs <- err
		}()
	}
	close(start)
	<-entered
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Call: %v", err)
		}
	}
	if probes.Load() != 1 {
		t.Errorf("probe count = %d, want 1", probes.Load())
	}
}

func decodeRequest(t *testing.T, r *http.Request) Request {
	t.Helper()
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Errorf("decode request: %v", err)
	}
	return req
}

func assertDiscoverRequest(t *testing.T, r *http.Request, req Request) {
	t.Helper()
	// Runs on the server's handler goroutine, where Fatal would leave the client waiting for a reply.
	if req.Method != "server/discover" || req.ID == nil {
		t.Errorf("request = %+v, want discover with an id", req)
		return
	}
	if r.Header.Get("MCP-Protocol-Version") != ModernVersions[0] || r.Header.Get("Mcp-Method") != "server/discover" {
		t.Errorf("probe headers = %q/%q", r.Header.Get("MCP-Protocol-Version"), r.Header.Get("Mcp-Method"))
	}
	if r.Header.Get("Mcp-Session-Id") != "" {
		t.Errorf("probe carried session id %q", r.Header.Get("Mcp-Session-Id"))
	}
	var params discoverRequestParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Errorf("decode probe params: %v", err)
		return
	}
	if params.Meta.ProtocolVersion != ModernVersions[0] ||
		params.Meta.ClientInfo.Name != "mini" || params.Meta.ClientInfo.Version == "" ||
		len(params.Meta.Capabilities) != 0 {
		t.Errorf("probe meta = %+v", params.Meta)
	}
}

type discoverRequestMeta struct {
	ProtocolVersion string         `json:"io.modelcontextprotocol/protocolVersion"`
	ClientInfo      ClientInfo     `json:"io.modelcontextprotocol/clientInfo"`
	Capabilities    map[string]any `json:"io.modelcontextprotocol/clientCapabilities"`
}

type discoverRequestParams struct {
	Meta discoverRequestMeta `json:"_meta"`
}

func serveProbeCall(t *testing.T, w http.ResponseWriter, req Request) {
	t.Helper()
	switch req.Method {
	case "initialize":
		writeRPCResult(t, w, req.ID, InitializeResult{
			ProtocolVersion: LegacyVersions[0],
			Capabilities:    map[string]any{},
			ServerInfo:      ServerInfo{Name: "fake", Version: "1"},
		})
	case NotificationInitialized:
		w.WriteHeader(http.StatusAccepted)
	default:
		writeRPCResult(t, w, req.ID, map[string]bool{"ok": true})
	}
}
