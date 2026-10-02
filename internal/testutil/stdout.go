package testutil

import (
	"io"
	"os"
	"testing"
)

type pipeRead struct {
	data []byte
	err  error
}

// CaptureStdout returns what fn writes to os.Stdout. It reads while fn runs, so output larger
// than the pipe's buffer can't block fn.
func CaptureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = stdout
		_ = w.Close() // already closed unless fn stopped the test; then this ends the read
		_ = r.Close() // the read already finished, or fn stopped the test and nothing waits for it
	})
	read := make(chan pipeRead, 1)
	go func() {
		data, err := io.ReadAll(r)
		read <- pipeRead{data, err}
	}()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got := <-read
	if got.err != nil {
		t.Fatal(got.err)
	}
	return string(got.data)
}
