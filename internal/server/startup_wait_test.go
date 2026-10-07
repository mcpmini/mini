//go:build test

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
)

const (
	holdTimer    = 1
	backoffTimer = 1
)

func gatedUpstream(t *testing.T) (url string, release func()) {
	t.Helper()
	return gatedUpstreamServing(t, pingMCPHandler)
}

func gatedUpstreamServing(t *testing.T, handler http.HandlerFunc) (url string, release func()) {
	t.Helper()
	gate := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-gate
		handler(w, r)
	}))
	released := false
	release = func() {
		if !released {
			released = true
			close(gate)
		}
	}
	t.Cleanup(ts.Close)
	t.Cleanup(release)
	return ts.URL, release
}

type heldRequest struct {
	session testSession
	output  chan []byte
}

func inBackground(srv *server.Server, session testSession) heldRequest {
	held := heldRequest{session: session, output: make(chan []byte, 1)}
	go func() { held.output <- session.run(srv) }()
	return held
}

func proxySession(t *testing.T, input []byte) testSession {
	t.Helper()
	return newTestSession(t, false, input)
}

func compactSession(t *testing.T, input []byte) testSession {
	t.Helper()
	return newTestSession(t, true, input)
}

func responseText(resp map[string]any) string {
	text, _ := json.Marshal(resp) //nolint:errcheck // decoded from JSON, so it encodes again
	return string(text)
}

func (r startupRetry) waitUntilHeld(t *testing.T, held heldRequest, otherTimers int) {
	t.Helper()
	if err := r.clock.BlockUntilContext(t.Context(), responseCleanupTimer+holdTimer+otherTimers); err != nil {
		t.Fatalf("waiting for the request to be held: %v", err)
	}
	select {
	case output := <-held.output:
		t.Fatalf("answered while a server was still connecting: %s", output)
	default:
	}
}

func (h heldRequest) answer(t *testing.T) string {
	t.Helper()
	select {
	case output := <-h.output:
		return responseText(h.session.response(t, output))
	case <-time.After(5 * time.Second):
		t.Fatal("held request wasn't answered within 5s")
		return ""
	}
}

func TestProxyToolsList_heldUntilAConnectingServerIsReady(t *testing.T) {
	url, release := gatedUpstream(t)
	r := connectWithFakeClock(t, httpServer("svc", url))

	held := inBackground(r.srv, proxySession(t, rpc("tools/list", nil)))
	r.waitUntilHeld(t, held, 0)
	release()

	if got := held.answer(t); !strings.Contains(got, "svc__ping") {
		t.Errorf("tools/list = %s, want svc's tools", got)
	}
}

func TestProxyToolsList_heldThroughARetryableFailureInsideTheWindow(t *testing.T) {
	ts, _ := upstreamFailingFirst(t, 1, pingMCPHandler)
	r := connectWithFakeClock(t, httpServer("svc", ts.URL))
	r.waitForBackoffTimer(t)

	held := inBackground(r.srv, proxySession(t, rpc("tools/list", nil)))
	r.waitUntilHeld(t, held, backoffTimer)
	r.clock.Advance(time.Second)

	if got := held.answer(t); !strings.Contains(got, "svc__ping") {
		t.Errorf("tools/list = %s, want svc's tools after its retry connected", got)
	}
}

func TestProxyToolsList_notHeldForAServerThatFailedForGood(t *testing.T) {
	needsAuth := httptest.NewServer(http.HandlerFunc(requireBearer))
	t.Cleanup(needsAuth.Close)
	r := connectWithFakeClock(t, httpServer("svc", needsAuth.URL))
	r.srv.WaitForStartupConnects()

	if got := responseText(serveProxy(t, r.srv, rpc("tools/list", nil))); strings.Contains(got, `"error"`) {
		t.Errorf("tools/list = %s, want the list", got)
	}
}

func TestProxyToolsList_aServerThatNeverConnectsReleasesTheListAtItsWindowEnd(t *testing.T) {
	url, _ := gatedUpstream(t)
	r := connectWithFakeClock(t, httpServer("svc", url))
	if err := r.srv.AddConnection(
		context.Background(),
		config.ServerConfig{Name: "ready"},
		fakeConn("ping"),
	); err != nil {
		t.Fatal(err)
	}

	held := inBackground(r.srv, proxySession(t, rpc("tools/list", nil)))
	r.waitUntilHeld(t, held, 0)
	r.clock.Advance(startupHold)

	if got := held.answer(t); !strings.Contains(got, "ready__ping") || strings.Contains(got, "svc__") {
		t.Errorf("tools/list at the window end = %s, want the connected servers' tools", got)
	}
	if got := responseText(serveProxy(t, r.srv, rpc("tools/list", nil))); !strings.Contains(got, "ready__ping") {
		t.Errorf("a later tools/list = %s, want it answered at once", got)
	}
}

func TestCompactDiscovery_fullListingsAreHeldWhileAServerIsConnecting(t *testing.T) {
	cases := map[string]map[string]any{
		"list":   {},
		"search": {"query": "ping"},
		"hidden": {"hidden": true},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			url, release := gatedUpstream(t)
			r := connectWithFakeClock(t, httpServer("svc", url))

			held := inBackground(r.srv, compactSession(t, callTool("list", args)))
			r.waitUntilHeld(t, held, 0)
			release()

			if got := held.answer(t); !strings.Contains(got, "svc.ping") {
				t.Errorf("list(%v) = %s, want svc's tools", args, got)
			}
		})
	}
}

func TestCompactDiscovery_metaToolsAndDetailLookupsAreNotHeld(t *testing.T) {
	url, _ := gatedUpstream(t)
	r := connectWithFakeClock(t, httpServer("svc", url))
	if err := r.srv.AddConnection(
		context.Background(),
		config.ServerConfig{Name: "ready"},
		fakeConn("ping"),
	); err != nil {
		t.Fatal(err)
	}

	if got := responseText(serve(t, r.srv, rpc("tools/list", nil))); !strings.Contains(got, `"perm_call"`) {
		t.Errorf("compact tools/list = %s, want the meta-tools", got)
	}
	detail := callTool("list", map[string]any{"tool": "ready.ping", "detail": true})
	if got := responseText(serve(t, r.srv, detail)); !strings.Contains(got, "ready.ping") {
		t.Errorf("detail lookup = %s, want ready.ping", got)
	}
}

func TestProxyToolsList_closeReleasesAHeldList(t *testing.T) {
	url, _ := gatedUpstream(t)
	r := connectWithFakeClock(t, httpServer("svc", url))

	held := inBackground(r.srv, proxySession(t, rpc("tools/list", nil)))
	r.waitUntilHeld(t, held, 0)
	mustCloseWithin(t, r.srv, 3*time.Second)

	if got := held.answer(t); !strings.Contains(got, "shutting down") {
		t.Errorf("tools/list held at Close = %s, want a shutdown error", got)
	}
}

func TestProxyToolsList_heldUntilTheLastConnectingServerIsReady(t *testing.T) {
	firstURL, releaseFirst := gatedUpstream(t)
	secondURL, releaseSecond := gatedUpstream(t)
	r := connectWithFakeClock(t, httpServer("first", firstURL), httpServer("second", secondURL))

	held := inBackground(r.srv, proxySession(t, rpc("tools/list", nil)))
	r.waitUntilHeld(t, held, 0)
	releaseFirst()
	eventually(t, func() bool { return r.srv.ToolCount("first") > 0 })
	r.waitUntilHeld(t, held, 0)
	releaseSecond()

	if got := held.answer(t); !strings.Contains(got, "first__ping") || !strings.Contains(got, "second__ping") {
		t.Errorf("tools/list = %s, want both servers' tools", got)
	}
}

func TestProxyToolsList_aHeldListIsReleasedWhenTheServerStopsConnecting(t *testing.T) {
	cases := []struct {
		name string
		stop func(r startupRetry, release func())
	}{
		{name: "it fails for good", stop: func(_ startupRetry, release func()) { release() }},
		{name: "it is removed", stop: func(r startupRetry, _ func()) {
			serve(t, r.srv, callTool("config", map[string]any{"action": "remove_server", "server": "svc"}))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, release := gatedUpstreamServing(t, requireBearer)
			r := connectWithFakeClock(t, httpServer("svc", url))

			held := inBackground(r.srv, proxySession(t, rpc("tools/list", nil)))
			r.waitUntilHeld(t, held, 0)
			tc.stop(r, release)

			if got := held.answer(t); strings.Contains(got, `"error"`) {
				t.Errorf("tools/list = %s, want the list", got)
			}
		})
	}
}
