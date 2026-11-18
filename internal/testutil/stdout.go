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
	return capture(t, &os.Stdout, fn)
}

// CaptureStderr is CaptureStdout for os.Stderr.
func CaptureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return capture(t, &os.Stderr, fn)
}

func capture(t *testing.T, stream **os.File, fn func()) string {
	t.Helper()
	r, w := redirect(t, stream)
	read := readAllAsync(r)
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

func redirect(t *testing.T, stream **os.File) (r, w *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := *stream
	*stream = w
	t.Cleanup(func() {
		*stream = original
		_ = w.Close() // already closed unless fn stopped the test; then this ends the read
		_ = r.Close() // the read already finished, or fn stopped the test and nothing waits for it
	})
	return r, w
}

func readAllAsync(r io.Reader) <-chan pipeRead {
	read := make(chan pipeRead, 1)
	go func() {
		data, err := io.ReadAll(r)
		read <- pipeRead{data, err}
	}()
	return read
}
