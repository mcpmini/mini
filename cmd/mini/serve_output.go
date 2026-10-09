package main

import (
	"io"
	"sync"
)

type serveOutput struct {
	mu     sync.Mutex
	out    io.Writer
	cancel func()
	in     io.Closer
	err    error
	stop   sync.Once
}

func newServeOutput(out io.Writer, cancel func(), in io.Closer) *serveOutput {
	return &serveOutput{out: out, cancel: cancel, in: in}
}

func (w *serveOutput) Write(p []byte) (int, error) {
	n, err := w.out.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.ResponseFailed(err)
	}
	return n, err
}

func (w *serveOutput) ResponseFailed(err error) {
	w.mu.Lock()
	if w.err == nil {
		w.err = err
	}
	w.mu.Unlock()
	w.stop.Do(w.cancelAndUnblockServe)
}

func (w *serveOutput) cancelAndUnblockServe() {
	w.cancel()
	_ = w.in.Close()
}

func (w *serveOutput) result(serveErr error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	return serveErr
}
