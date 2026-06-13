package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func timeoutAfter() <-chan time.Time {
	return time.After(5 * time.Second)
}

func toolCallLine() string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}` + "\n"
}

func okHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var msg struct {
		Method string `json:"method"`
	}
	json.Unmarshal(body, &msg) //nolint:errcheck
	switch msg.Method {
	case "initialize":
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":-1,"result":{"protocolVersion":"2025-03-26"}}`)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	default:
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"ok"}`)
	}
}

func TestDeliver_transportDownRecoversViaReresolve(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(okHandler))
	defer live.Close()
	livePort := serverPort(t, live)

	var resolveCalls atomic.Int32
	reresolve := func() (int, string, error) {
		resolveCalls.Add(1)
		return livePort, "tok", nil
	}
	in := strings.NewReader(toolCallLine())
	var out bytes.Buffer
	p := RunParams{Port: closedPort(t), SessionID: "sess", Token: "tok", In: in, Out: &out, Reresolve: reresolve}
	if err := Run(p); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), `"result":"ok"`) {
		t.Errorf("expected recovered result, got %q", out.String())
	}
	if resolveCalls.Load() != 1 {
		t.Errorf("Reresolve calls = %d, want 1", resolveCalls.Load())
	}
}

func TestDeliver_unauthorizedRefreshesTokenAndRetries(t *testing.T) {
	var seenTokens []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		seenTokens = append(seenTokens, auth)
		mu.Unlock()
		if auth != "Bearer fresh" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		okHandler(w, r)
	}))
	defer srv.Close()
	port := serverPort(t, srv)

	reresolve := func() (int, string, error) { return port, "fresh", nil }
	in := strings.NewReader(toolCallLine())
	var out bytes.Buffer
	p := RunParams{Port: port, SessionID: "sess", Token: "stale", In: in, Out: &out, Reresolve: reresolve}
	if err := Run(p); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), `"result":"ok"`) {
		t.Errorf("expected ok after token refresh, got %q", out.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if seenTokens[0] != "Bearer stale" || !containsToken(seenTokens, "Bearer fresh") {
		t.Errorf("expected stale then fresh bearer, got %v", seenTokens)
	}
}

func containsToken(tokens []string, want string) bool {
	for _, tk := range tokens {
		if tk == want {
			return true
		}
	}
	return false
}

// hijackCloseHandler accepts the connection then closes it without writing a response,
// producing a post-send client.Do error (not a dial refusal) — the otherTransportError case.
func hijackCloseHandler(w http.ResponseWriter, _ *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	conn.Close()
}

func TestDeliver_midFlightErrorDoesNotRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(hijackCloseHandler))
	defer srv.Close()

	var resolveCalls atomic.Int32
	reresolve := func() (int, string, error) {
		resolveCalls.Add(1)
		return serverPort(t, srv), "tok", nil
	}
	in := strings.NewReader(toolCallLine())
	var out bytes.Buffer
	p := RunParams{Port: serverPort(t, srv), SessionID: "sess", Token: "tok", In: in, Out: &out, Reresolve: reresolve}
	if err := Run(p); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resolveCalls.Load() != 0 {
		t.Errorf("Reresolve must NOT be called for a post-send error, got %d calls", resolveCalls.Load())
	}
	if !strings.Contains(out.String(), `"error"`) || strings.Count(out.String(), "\n") != 1 {
		t.Errorf("expected exactly one error response, got %q", out.String())
	}
}

func TestDeliver_singleFlightRecoversOnce(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(okHandler))
	defer live.Close()
	livePort := serverPort(t, live)
	dead := closedPort(t)

	var resolveCalls atomic.Int32
	reresolve := func() (int, string, error) {
		resolveCalls.Add(1)
		return livePort, "tok", nil
	}
	const n = 16
	var lines strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&lines, `{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{}}`+"\n", i)
	}
	var out bytes.Buffer
	var outMu sync.Mutex
	p := RunParams{Port: dead, SessionID: "sess", Token: "tok", In: strings.NewReader(lines.String()), Out: &lockedWriter{w: &out, mu: &outMu}, Reresolve: reresolve}
	if err := Run(p); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := resolveCalls.Load(); got != 1 {
		t.Errorf("Reresolve calls = %d, want exactly 1 (single-flight)", got)
	}
	if got := strings.Count(out.String(), `"result":"ok"`); got != n {
		t.Errorf("recovered results = %d, want %d", got, n)
	}
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

func TestDeliver_boundedWhenReresolveKeepsFailing(t *testing.T) {
	var resolveCalls atomic.Int32
	reresolve := func() (int, string, error) {
		resolveCalls.Add(1)
		return 0, "", fmt.Errorf("daemon down")
	}
	in := strings.NewReader(toolCallLine())
	var out bytes.Buffer
	p := RunParams{Port: closedPort(t), SessionID: "sess", Token: "tok", In: in, Out: &out, Reresolve: reresolve}
	done := make(chan error, 1)
	go func() { done <- Run(p) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-timeoutAfter():
		t.Fatal("Run did not return — recovery is not bounded")
	}
	if !strings.Contains(out.String(), `"error"`) {
		t.Errorf("expected error response on exhaustion, got %q", out.String())
	}
}

// TestDeliver_boundedWhenRespawnedDaemonStaysDead covers the case where Reresolve SUCCEEDS
// every time but always hands back a dead port (a respawned daemon that never comes up).
// Recovery must still terminate within the bounded attempts and return a clean error,
// rather than spinning forever — complements the Reresolve-erroring case above.
func TestDeliver_boundedWhenRespawnedDaemonStaysDead(t *testing.T) {
	dead := closedPort(t)
	var resolveCalls atomic.Int32
	reresolve := func() (int, string, error) {
		resolveCalls.Add(1)
		return dead, "tok", nil
	}
	in := strings.NewReader(toolCallLine())
	var out bytes.Buffer
	p := RunParams{Port: dead, SessionID: "sess", Token: "tok", In: in, Out: &out, Reresolve: reresolve}
	done := make(chan error, 1)
	go func() { done <- Run(p) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-timeoutAfter():
		t.Fatal("Run did not return — recovery is not bounded when respawn stays dead")
	}
	if !strings.Contains(out.String(), `"error"`) {
		t.Errorf("expected error response on exhaustion, got %q", out.String())
	}
	if resolveCalls.Load() == 0 {
		t.Error("expected at least one Reresolve attempt before giving up")
	}
}
