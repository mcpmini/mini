package testutil

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func StartUnixServer(t *testing.T, sock string, h http.HandlerFunc) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(sock), 0700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", sock)
	if err != nil {
		t.Fatalf("listen unix %s: %v", sock, err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)                  //nolint:errcheck
	t.Cleanup(func() { srv.Close() }) //nolint:errcheck
}
