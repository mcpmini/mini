package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	var port int
	fmt.Sscanf(u.Port(), "%d", &port)
	return port
}

func closedPort(t *testing.T) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	port := serverPort(t, srv)
	srv.Close()
	return port
}

func TestDaemonErrorResponse_withIdReturnsEnvelope(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`)
	resp := daemonErrorResponse(body, "boom")
	if resp == nil {
		t.Fatal("expected non-nil response for request with id")
	}
	s := string(resp)
	if !strings.Contains(s, `"error"`) || !strings.Contains(s, "boom") || !strings.Contains(s, `"id":1`) {
		t.Errorf("unexpected response: %s", s)
	}
}

func TestDaemonErrorResponse_notificationReturnsNil(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if resp := daemonErrorResponse(body, "irrelevant"); resp != nil {
		t.Errorf("expected nil for notification (no id), got %s", resp)
	}
}

func TestDaemonErrorResponse_malformedBodyReturnsNil(t *testing.T) {
	if resp := daemonErrorResponse([]byte(`not json`), "msg"); resp != nil {
		t.Errorf("expected nil for unparseable body, got %s", resp)
	}
}

func TestForward_successReturnsDaemonResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	}))
	defer srv.Close()
	resp := forward(&http.Client{}, serverPort(t, srv), "sess", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`))
	if !strings.Contains(string(resp), `"result":"ok"`) {
		t.Errorf("unexpected response: %s", resp)
	}
}

func TestForward_daemonUnreachableReturnsErrorEnvelope(t *testing.T) {
	resp := forward(&http.Client{}, closedPort(t), "sess", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`))
	if resp == nil || !strings.Contains(string(resp), `"error"`) {
		t.Errorf("expected error envelope, got %s", resp)
	}
}

func TestForward_202NotificationReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	resp := forward(&http.Client{}, serverPort(t, srv), "sess", []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if resp != nil {
		t.Errorf("expected nil for 202 Accepted, got %s", resp)
	}
}

func TestForward_sessionIdPropagatedInHeader(t *testing.T) {
	var gotSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSession = r.Header.Get("Mcp-Session-Id")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	}))
	defer srv.Close()
	forward(&http.Client{}, serverPort(t, srv), "my-session-42", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if gotSession != "my-session-42" {
		t.Errorf("session header = %q, want %q", gotSession, "my-session-42")
	}
}

func TestForward_largeResponseBodyHandledWithoutError(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, big)
	}))
	defer srv.Close()
	resp := forward(&http.Client{}, serverPort(t, srv), "sess", []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	if len(resp) == 0 {
		t.Error("expected non-empty response for large body")
	}
}

func TestRun_routesRequestAndWritesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"done"}`)
	}))
	defer srv.Close()
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}` + "\n")
	var out bytes.Buffer
	if err := Run(serverPort(t, srv), "sess", in, &out); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !strings.Contains(out.String(), "done") {
		t.Errorf("unexpected output: %q", out.String())
	}
}

func TestRun_emptyLinesSkipped(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	}))
	defer srv.Close()
	in := strings.NewReader("\n\n" + `{"jsonrpc":"2.0","id":1,"method":"tools/call"}` + "\n\n")
	Run(serverPort(t, srv), "sess", in, io.Discard) //nolint:errcheck
	if calls.Load() != 1 {
		t.Errorf("expected 1 daemon call (empty lines skipped), got %d", calls.Load())
	}
}

func TestRun_cleanEOFReturnsNilError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	if err := Run(serverPort(t, srv), "sess", strings.NewReader(""), io.Discard); err != nil {
		t.Errorf("expected nil error on clean EOF, got %v", err)
	}
}

func TestRun_multipleRequestsAllForwarded(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"ok"}`, n)
	}))
	defer srv.Close()
	in := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"a"}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"b"}` + "\n",
	)
	if err := Run(serverPort(t, srv), "sess", in, io.Discard); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 daemon calls, got %d", calls.Load())
	}
}

func newConcurrencyServer(t *testing.T, release chan struct{}) (*httptest.Server, *atomic.Int32, <-chan struct{}) {
	t.Helper()
	var current, maxSeen atomic.Int32
	var once sync.Once
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := current.Add(1)
		for {
			prev := maxSeen.Load()
			if n <= prev || maxSeen.CompareAndSwap(prev, n) {
				break
			}
		}
		once.Do(func() { started <- struct{}{} })
		<-release
		current.Add(-1)
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &maxSeen, started
}

func TestRunWithLimit_capsConcurrentForwards(t *testing.T) {
	release := make(chan struct{})
	srv, maxSeen, started := newConcurrencyServer(t, release)
	done := make(chan error, 1)
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"a"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"b"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"c"}` + "\n")
	go func() { done <- runWithLimit(serverPort(t, srv), "sess", input, io.Discard, 1) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first request to reach daemon")
	}
	if got := maxSeen.Load(); got != 1 {
		t.Fatalf("max concurrent forwards = %d, want 1", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("runWithLimit error: %v", err)
	}
	if got := maxSeen.Load(); got != 1 {
		t.Fatalf("max concurrent forwards after completion = %d, want 1", got)
	}
}
