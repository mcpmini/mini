//go:build test

package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestHTTPStream_notificationArrivesAfterPublishedCatalog(t *testing.T) {
	srv := newRefreshTestServer(t)
	conn := newNotificationFake(toolDef("old", `{"type":"object"}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	<-conn.listCall
	ts := httptest.NewServer(srv)
	defer ts.Close()

	sessionID := initHTTPProxySession(t, ts, "")
	stream := openHTTPStream(t, ts, sessionID)
	defer stream.Body.Close()

	conn.replace(toolDef("new", `{"type":"object"}`))
	mustReadNotification(t, stream.Body, time.Second)
	names := httpToolNames(t, ts, sessionID)
	assertContainsName(t, names, "svc__new")
	assertMissingName(t, names, "svc__old")
}

func TestHTTPStream_initializedBeforeInitializeDoesNotMarkClientReady(t *testing.T) {
	srv := newRefreshTestServer(t)
	conn := newNotificationFake(toolDef("old", `{"type":"object"}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	<-conn.listCall
	ts := httptest.NewServer(srv)
	defer ts.Close()

	sessionID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	postNotification(t, ts, sessionID, transport.NotificationInitialized)
	initializeOnly(t, ts, sessionID)
	stream := openHTTPStream(t, ts, sessionID)

	conn.replace(toolDef("new", `{"type":"object"}`))
	assertNoNotification(t, stream.Body, 150*time.Millisecond)
	stream.Body.Close()

	postNotification(t, ts, sessionID, transport.NotificationInitialized)
	stream = openHTTPStream(t, ts, sessionID)
	defer stream.Body.Close()
	conn.replace(toolDef("newer", `{"type":"object"}`))
	mustReadNotification(t, stream.Body, time.Second)
}

func TestHTTPStream_multipleGETsReceiveSingleDelivery(t *testing.T) {
	srv := newRefreshTestServer(t)
	conn := newNotificationFake(toolDef("old", `{"type":"object"}`))
	if err := srv.AddConnection(t.Context(), config.ServerConfig{Name: "svc"}, conn); err != nil {
		t.Fatal(err)
	}
	<-conn.listCall
	ts := httptest.NewServer(srv)
	defer ts.Close()

	sessionID := initHTTPProxySession(t, ts, "")
	streamA := openHTTPStream(t, ts, sessionID)
	defer streamA.Body.Close()
	streamB := openHTTPStream(t, ts, sessionID)
	defer streamB.Body.Close()

	conn.replace(toolDef("new", `{"type":"object"}`))
	received := 0
	if hasNotification(t, streamA.Body, 250*time.Millisecond) {
		received++
	}
	if hasNotification(t, streamB.Body, 250*time.Millisecond) {
		received++
	}
	if received != 1 {
		t.Fatalf("received %d notifications, want exactly 1", received)
	}
}

func TestHTTPStream_beginShutdownClosesOpenStream(t *testing.T) {
	srv := newRefreshTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	sessionID := initHTTPProxySession(t, ts, "")
	stream := openHTTPStream(t, ts, sessionID)
	defer stream.Body.Close()

	closed := make(chan error, 1)
	go func() {
		_, err := readNotification(stream.Body)
		closed <- err
	}()

	srv.BeginShutdown()
	select {
	case err := <-closed:
		if err == nil || !strings.Contains(err.Error(), "stream closed before notification") {
			t.Fatalf("readNotification() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("open stream stayed alive after shutdown")
	}
}

func TestHTTPStream_beginShutdownRejectsNewStream(t *testing.T) {
	srv := newRefreshTestServer(t)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	sessionID := initHTTPProxySession(t, ts, "")
	srv.BeginShutdown()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}

func initHTTPProxySession(t *testing.T, ts *httptest.Server, sessionID string) string {
	t.Helper()
	sessionID = initializeOnly(t, ts, sessionID)
	postNotification(t, ts, sessionID, transport.NotificationInitialized)
	return sessionID
}

func initializeOnly(t *testing.T, ts *httptest.Server, sessionID string) string {
	t.Helper()
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	}
	resp := postHTTPRPC(t, ts, sessionID, body)
	defer resp.Body.Close()
	return responseSessionID(t, resp, sessionID)
}

func postNotification(t *testing.T, ts *httptest.Server, sessionID, method string) {
	t.Helper()
	resp := postHTTPRPC(t, ts, sessionID, map[string]any{"jsonrpc": "2.0", "method": method})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("notification %s status = %d", method, resp.StatusCode)
	}
}

func postHTTPRPC(t *testing.T, ts *httptest.Server, sessionID string, body map[string]any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func responseSessionID(t *testing.T, resp *http.Response, fallback string) string {
	t.Helper()
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		return id
	}
	return fallback
}

func openHTTPStream(t *testing.T, ts *httptest.Server, sessionID string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("stream status = %d body=%s", resp.StatusCode, body)
	}
	return resp
}

func httpToolNames(t *testing.T, ts *httptest.Server, sessionID string) []string {
	t.Helper()
	resp := postHTTPRPC(t, ts, sessionID, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	defer resp.Body.Close()
	var rpc struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(rpc.Result.Tools))
	for _, tool := range rpc.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func mustReadNotification(t *testing.T, body io.Reader, timeout time.Duration) transport.Notification {
	t.Helper()
	type result struct {
		notification transport.Notification
		err          error
	}
	done := make(chan result, 1)
	go func() {
		notification, err := readNotification(body)
		done <- result{notification: notification, err: err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatal(res.err)
		}
		return res.notification
	case <-time.After(timeout):
		t.Fatal("timed out waiting for notification")
		return transport.Notification{}
	}
}

func hasNotification(t *testing.T, body io.Reader, timeout time.Duration) bool {
	t.Helper()
	ch := make(chan bool, 1)
	go func() {
		notification, err := readNotification(body)
		ch <- err == nil && notification.Method != ""
	}()
	select {
	case ok := <-ch:
		return ok
	case <-time.After(timeout):
		return false
	}
}

func assertNoNotification(t *testing.T, body io.Reader, timeout time.Duration) {
	t.Helper()
	if hasNotification(t, body, timeout) {
		t.Fatal("unexpected notification")
	}
}

func assertContainsName(t *testing.T, names []string, want string) {
	t.Helper()
	for _, name := range names {
		if name == want {
			return
		}
	}
	t.Fatalf("missing tool %q in %v", want, names)
}

func assertMissingName(t *testing.T, names []string, want string) {
	t.Helper()
	for _, name := range names {
		if name == want {
			t.Fatalf("unexpected tool %q in %v", want, names)
		}
	}
}

func readNotification(body io.Reader) (transport.Notification, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(data) == 0 {
				continue
			}
			var notification transport.Notification
			if err := json.Unmarshal([]byte(strings.Join(data, "\n")), &notification); err != nil {
				return transport.Notification{}, err
			}
			return notification, nil
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimSpace(value))
		}
	}
	if err := scanner.Err(); err != nil {
		return transport.Notification{}, err
	}
	return transport.Notification{}, errors.New("stream closed before notification")
}
