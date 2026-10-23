package daemon_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/daemon"
	"github.com/mcpmini/mini/internal/testutil"
)

func shortConfigDir(t *testing.T) string { return testutil.ShortTempDir(t) }

func TestEnsureToken_reusesExistingNonEmptyToken(t *testing.T) {
	dir := t.TempDir()
	first, err := daemon.WriteToken(dir)
	if err != nil {
		t.Fatalf("WriteToken: %v", err)
	}
	got, err := daemon.EnsureToken(dir)
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if got != first {
		t.Errorf("EnsureToken rotated token: got %q, want existing %q", got, first)
	}
}

func TestEnsureToken_mintsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	got, err := daemon.EnsureToken(dir)
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if got == "" {
		t.Fatal("EnsureToken returned empty token when none existed")
	}
	persisted, err := daemon.ReadToken(dir)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if persisted != got {
		t.Errorf("minted token not persisted: file=%q, returned=%q", persisted, got)
	}
}

func TestEnsureToken_reMintsOnLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	path := daemon.TokenFile(dir)
	testutil.WriteFile(t, path, "loose-secret")
	if err := os.Chmod(path, 0o644); err != nil { // chmod ignores umask, forcing loose perms
		t.Fatal(err)
	}
	got, err := daemon.EnsureToken(dir)
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if got == "loose-secret" {
		t.Error("EnsureToken reused a group/other-readable token instead of re-minting")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("re-minted token perm = %#o, want 0600", info.Mode().Perm())
	}
}

func TestEnsureToken_mintsWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	p := daemon.TokenFile(dir)
	testutil.WriteFile(t, p, "   ")
	got, err := daemon.EnsureToken(dir)
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if got == "" {
		t.Fatal("EnsureToken returned empty token for whitespace-only file")
	}
}

func TestSocketPath(t *testing.T) {
	got := daemon.SocketPath("/my/config")
	want := filepath.Join("/my/config", "internal", "daemon", "daemon.sock")
	if got != want {
		t.Errorf("SocketPath = %q, want %q", got, want)
	}
}

func TestCheckSocketPath_shortPathOK(t *testing.T) {
	if err := daemon.CheckSocketPath("/tmp/mini"); err != nil {
		t.Errorf("CheckSocketPath(short) = %v, want nil", err)
	}
}

func TestCheckSocketPath_tooLongRejected(t *testing.T) {
	dir := "/tmp/" + strings.Repeat("x", 120)
	err := daemon.CheckSocketPath(dir)
	if err == nil {
		t.Fatal("CheckSocketPath(long) = nil, want error")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("error = %q, want it to mention the path is too long", err)
	}
}

func TestRunning_noSocketReturnsFalse(t *testing.T) {
	if daemon.Running(shortConfigDir(t)) {
		t.Error("expected false when no socket exists")
	}
}

func TestRunning_healthyDaemonReturnsTrue(t *testing.T) {
	dir := shortConfigDir(t)
	testutil.StartUnixServer(t, daemon.SocketPath(dir), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if !daemon.Running(dir) {
		t.Error("expected true for healthy daemon")
	}
}

func TestRunning_non200ReturnsFalse(t *testing.T) {
	dir := shortConfigDir(t)
	testutil.StartUnixServer(t, daemon.SocketPath(dir), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if daemon.Running(dir) {
		t.Error("expected false for non-200 daemon")
	}
}

func TestRunning_staleSocketFileReturnsFalse(t *testing.T) {
	dir := shortConfigDir(t)
	sp := daemon.SocketPath(dir)
	testutil.WriteFile(t, sp, "")
	if daemon.Running(dir) {
		t.Error("expected false for a non-socket file at the socket path")
	}
}

func TestRunning_checksHealthzPath(t *testing.T) {
	dir := shortConfigDir(t)
	gotPath := make(chan string, 1)
	testutil.StartUnixServer(t, daemon.SocketPath(dir), func(w http.ResponseWriter, r *http.Request) {
		gotPath <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	daemon.Running(dir)
	select {
	case p := <-gotPath:
		if p != "/healthz" {
			t.Errorf("expected /healthz path, got %q", p)
		}
	case <-time.After(time.Second):
		t.Fatal("healthcheck did not call the daemon")
	}
}

func TestSocketClient_dialsTheDaemonForEveryRequest(t *testing.T) {
	sock := daemon.SocketPath(shortConfigDir(t))
	testutil.StartUnixServer(t, sock, func(http.ResponseWriter, *http.Request) {})
	client := daemon.SocketClient(sock, 5*time.Second)
	for i := range 2 {
		if socketClientReusedConnection(t, client) {
			t.Fatalf("request %d reused a pooled connection, which fails unretried if the daemon has exited since", i)
		}
	}
}

func socketClientReusedConnection(t *testing.T, client *http.Client) bool {
	t.Helper()
	var reused bool
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}
	ctx := httptrace.WithClientTrace(context.Background(), trace)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/mcp", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST to the daemon socket: %v", err)
	}
	//nolint:errcheck // draining only lets the transport pool the connection, which is what the test checks.
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close() //nolint:errcheck // the empty response is already read
	return reused
}
