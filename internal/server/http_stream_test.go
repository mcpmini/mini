//go:build test

package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
)

func openMCPStream(t *testing.T, ts *httptest.Server, sessionID string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/mcp", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := ts.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	return resp, cancel
}

func readSSEMethod(t *testing.T, resp *http.Response) string {
	t.Helper()
	out := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			data, ok := strings.CutPrefix(scanner.Text(), "data:")
			if !ok {
				continue
			}
			var n struct {
				Method string `json:"method"`
			}
			json.Unmarshal([]byte(strings.TrimSpace(data)), &n) //nolint:errcheck
			out <- n.Method
			return
		}
	}()
	select {
	case m := <-out:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an SSE notification on the stream")
		return ""
	}
}

func TestHTTPServer_streamReturnsSSEContentType(t *testing.T) {
	_, ts := newHTTPTestServer(t)
	sessionID := initSession(t, ts)
	resp, cancel := openMCPStream(t, ts, sessionID)
	defer cancel()
	defer resp.Body.Close()

	mustStatus(t, resp, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected SSE content type on the stream, got %q", ct)
	}
}

func TestHTTPServer_streamDeliversToolsChanged(t *testing.T) {
	srv, ts := newHTTPTestServer(t)
	srv.AddConnection(context.Background(), config.ServerConfig{Name: "svc"}, fakeConn("a")) //nolint:errcheck
	sessionID := initSession(t, ts)

	// Do() returns once headers flush, which the server does right after wiring
	// the session's notify channel — so the subsequent change cannot be missed.
	resp, cancel := openMCPStream(t, ts, sessionID)
	defer cancel()
	defer resp.Body.Close()

	removeBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "config", "arguments": map[string]any{
			"action": "remove_server", "server": "svc",
		}},
	})
	drainMCPPost(t, ts, removeBody, sessionID)

	if m := readSSEMethod(t, resp); m != "notifications/tools/list_changed" {
		t.Errorf("stream delivered method %q, want notifications/tools/list_changed", m)
	}
}

func TestHTTPServer_shutdownStreamsReleasesOpenStream(t *testing.T) {
	srv, ts := newHTTPTestServer(t)
	sessionID := initSession(t, ts)
	resp, cancel := openMCPStream(t, ts, sessionID)
	defer cancel()
	defer resp.Body.Close()
	mustStatus(t, resp, http.StatusOK)

	srv.ShutdownStreams()

	done := make(chan struct{})
	go func() {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("open stream was not released after ShutdownStreams")
	}
}
