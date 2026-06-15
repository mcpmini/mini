package daemon_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/mcpmini/mini/internal/daemon"
)

func writePortFile(t *testing.T, dir string, port int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "daemon.port"), []byte(fmt.Sprintf("%d", port)), 0644); err != nil {
		t.Fatal(err)
	}
}

func serverPort(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	var port int
	fmt.Sscanf(u.Port(), "%d", &port)
	return port
}

func TestPortFile(t *testing.T) {
	got := daemon.PortFile("/my/config")
	want := filepath.Join("/my/config", "daemon.port")
	if got != want {
		t.Errorf("PortFile = %q, want %q", got, want)
	}
}

func TestRunningPort_missingFile(t *testing.T) {
	if got := daemon.RunningPort(t.TempDir()); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestRunningPort_nonNumericContent(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "daemon.port"), []byte("not-a-port"), 0644) //nolint:errcheck
	if got := daemon.RunningPort(dir); got != 0 {
		t.Errorf("expected 0 for non-numeric port file, got %d", got)
	}
}

func TestRunningPort_healthyDaemonReturnsPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	port := serverPort(t, srv)
	dir := t.TempDir()
	writePortFile(t, dir, port)
	if got := daemon.RunningPort(dir); got != port {
		t.Errorf("RunningPort = %d, want %d", got, port)
	}
}

func TestRunningPort_non200ReturnsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	dir := t.TempDir()
	writePortFile(t, dir, serverPort(t, srv))
	if got := daemon.RunningPort(dir); got != 0 {
		t.Errorf("expected 0 for non-200 daemon, got %d", got)
	}
}

func TestRunningPort_connectionRefusedReturnsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	port := serverPort(t, srv)
	srv.Close()
	dir := t.TempDir()
	writePortFile(t, dir, port)
	if got := daemon.RunningPort(dir); got != 0 {
		t.Errorf("expected 0 for unreachable daemon, got %d", got)
	}
}

func TestRunningPort_checksHealthzPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	dir := t.TempDir()
	writePortFile(t, dir, serverPort(t, srv))
	daemon.RunningPort(dir)
	if gotPath != "/healthz" {
		t.Errorf("expected /healthz path, got %q", gotPath)
	}
}

func TestWriteToken_replacesLooseFileWith0600(t *testing.T) {
	configDir := t.TempDir()
	stale := daemon.TokenFile(configDir)
	if err := os.WriteFile(stale, []byte("old"), 0644); err != nil {
		t.Fatalf("seed stale token: %v", err)
	}

	token, err := daemon.WriteToken(configDir)
	if err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	info, err := os.Stat(stale)
	if err != nil {
		t.Fatalf("stat token: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("token mode = %o, want 0600 (must tighten the stale 0644 file)", perm)
	}

	got, err := daemon.ReadToken(configDir)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != token {
		t.Errorf("round-trip mismatch: read %q, wrote %q", got, token)
	}
	if got == "old" {
		t.Error("stale token was not replaced")
	}
}

func TestWriteToken_returnsDistinctTokens(t *testing.T) {
	configDir := t.TempDir()
	first, err := daemon.WriteToken(configDir)
	if err != nil {
		t.Fatalf("WriteToken: %v", err)
	}
	second, err := daemon.WriteToken(configDir)
	if err != nil {
		t.Fatalf("WriteToken: %v", err)
	}
	if first == second {
		t.Error("expected a freshly minted token on each call")
	}
}
