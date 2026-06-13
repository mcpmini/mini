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
	if err := os.WriteFile(path, []byte("loose-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil { // chmod ignores umask, forcing loose perms
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
	if info.Mode().Perm() != 0600 {
		t.Errorf("re-minted token perm = %#o, want 0600", info.Mode().Perm())
	}
}

func TestEnsureToken_mintsWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(daemon.TokenFile(dir), []byte("   "), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := daemon.EnsureToken(dir)
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if got == "" {
		t.Fatal("EnsureToken returned empty token for whitespace-only file")
	}
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
