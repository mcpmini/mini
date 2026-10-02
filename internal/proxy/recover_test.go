//go:build test

package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/daemon"
	"github.com/mcpmini/mini/internal/testutil"
)

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

func newSockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(shortSocketDir(t), "d.sock")
}

func TestDeliver_transportDownRecoversViaReresolve(t *testing.T) {
	sock := newSockPath(t)
	client := daemon.SocketClient(sock, 0)

	var resolveCalls atomic.Int32
	reresolve := func() (string, error) {
		resolveCalls.Add(1)
		testutil.StartUnixServer(t, sock, okHandler) // respawn the daemon on the same socket
		return "tok", nil
	}
	in := strings.NewReader(toolCallLine())
	var out bytes.Buffer
	p := RunParams{Client: client, SessionID: "sess", Token: "tok", In: in, Out: &out, Resolver: NewDaemonResolver(reresolve)}
	if err := runWithFakeClock(t, p); err != nil {
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
	client := serveSocket(t, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		seenTokens = append(seenTokens, auth)
		mu.Unlock()
		if auth != "Bearer fresh" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		okHandler(w, r)
	})

	reresolve := func() (string, error) { return "fresh", nil }
	in := strings.NewReader(toolCallLine())
	var out bytes.Buffer
	p := RunParams{Client: client, SessionID: "sess", Token: "stale", In: in, Out: &out, Resolver: NewDaemonResolver(reresolve)}
	if err := runWithFakeClock(t, p); err != nil {
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

// Produces a post-send client.Do error (not a dial failure) by hijacking and immediately closing — exercises the outcomeOther path.
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
	client := serveSocket(t, hijackCloseHandler)

	var resolveCalls atomic.Int32
	reresolve := func() (string, error) {
		resolveCalls.Add(1)
		return "tok", nil
	}
	in := strings.NewReader(toolCallLine())
	var out bytes.Buffer
	p := RunParams{Client: client, SessionID: "sess", Token: "tok", In: in, Out: &out, Resolver: NewDaemonResolver(reresolve), Clock: clock.NewFake()}
	if err := runWithFakeClock(t, p); err != nil {
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
	sock := newSockPath(t)
	client := daemon.SocketClient(sock, 0)

	var resolveCalls atomic.Int32
	reresolve := func() (string, error) {
		resolveCalls.Add(1)
		testutil.StartUnixServer(t, sock, okHandler)
		return "tok", nil
	}
	const n = 16
	var lines strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&lines, `{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{}}`+"\n", i)
	}
	var out bytes.Buffer
	p := RunParams{Client: client, SessionID: "sess", Token: "tok", In: strings.NewReader(lines.String()), Out: &out, Resolver: NewDaemonResolver(reresolve)}
	if err := runWithFakeClock(t, p); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := resolveCalls.Load(); got != 1 {
		t.Errorf("Reresolve calls = %d, want exactly 1 (single-flight)", got)
	}
	if got := strings.Count(out.String(), `"result":"ok"`); got != n {
		t.Errorf("recovered results = %d, want %d", got, n)
	}
}

func TestDeliver_boundedWhenReresolveKeepsFailing(t *testing.T) {
	var resolveCalls atomic.Int32
	reresolve := func() (string, error) {
		resolveCalls.Add(1)
		return "", fmt.Errorf("daemon down")
	}
	in := strings.NewReader(toolCallLine())
	var out bytes.Buffer
	p := RunParams{Client: deadClient(t), SessionID: "sess", Token: "tok", In: in, Out: &out, Resolver: NewDaemonResolver(reresolve)}
	if err := runWithFakeClock(t, p); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), `"error"`) {
		t.Errorf("expected error response on exhaustion, got %q", out.String())
	}
}

func TestDeliver_persistent401ReturnsErrorEnvelope(t *testing.T) {
	client := serveSocket(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "go away", http.StatusUnauthorized)
	})

	var resolveCalls atomic.Int32
	reresolve := func() (string, error) {
		resolveCalls.Add(1)
		return "same-stale-token", nil
	}
	in := strings.NewReader(toolCallLine())
	var out bytes.Buffer
	p := RunParams{Client: client, SessionID: "sess", Token: "same-stale-token", In: in, Out: &out, Resolver: NewDaemonResolver(reresolve)}
	if err := runWithFakeClock(t, p); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"error"`) {
		t.Errorf("expected JSON-RPC error envelope, got %q", got)
	}
	if strings.Contains(got, "go away") {
		t.Error("raw 401 body leaked verbatim to agent")
	}
}

func TestDeliver_boundedWhenRespawnedDaemonStaysDead(t *testing.T) {
	var resolveCalls atomic.Int32
	reresolve := func() (string, error) {
		resolveCalls.Add(1)
		return "tok", nil // token refreshes, but the daemon never comes back on the socket
	}
	in := strings.NewReader(toolCallLine())
	var out bytes.Buffer
	p := RunParams{Client: deadClient(t), SessionID: "sess", Token: "tok", In: in, Out: &out, Resolver: NewDaemonResolver(reresolve)}
	if err := runWithFakeClock(t, p); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), `"error"`) {
		t.Errorf("expected error response on exhaustion, got %q", out.String())
	}
	if resolveCalls.Load() == 0 {
		t.Error("expected at least one Reresolve attempt before giving up")
	}
}

func TestDeliver_singleFlightOnResolveFailure(t *testing.T) {
	const n = 8
	sock := newSockPath(t)
	client := daemon.SocketClient(sock, 0)

	var resolveCalls atomic.Int32
	reresolve := func() (string, error) {
		resolveCalls.Add(1)
		time.Sleep(50 * time.Millisecond) // simulate slow spawn that fails
		return "", fmt.Errorf("daemon down")
	}

	var lines strings.Builder
	for i := range n {
		fmt.Fprintf(&lines, `{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{}}`+"\n", i)
	}
	var out bytes.Buffer
	p := RunParams{Client: client, SessionID: "sess", Token: "tok", In: strings.NewReader(lines.String()), Out: &out, Resolver: NewDaemonResolver(reresolve)}
	if err := runWithFakeClock(t, p); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := resolveCalls.Load(); got > 3 {
		t.Errorf("Resolve calls = %d with %d goroutines; single-flight should prevent N×timeout stacking", got, n)
	}
	if !strings.Contains(out.String(), `"error"`) {
		t.Errorf("expected error responses on exhaustion, got: %q", out.String())
	}
}
