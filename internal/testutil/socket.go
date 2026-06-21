package testutil

import (
	"net"
	"net/http"
	"testing"
)

func StartUnixServer(t *testing.T, sock string, h http.HandlerFunc) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix %s: %v", sock, err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)                  //nolint:errcheck
	t.Cleanup(func() { srv.Close() }) //nolint:errcheck
}
