package proxy

import (
	"io"
	"sync"
)

type lineWriter struct {
	out io.Writer
	mu  sync.Mutex
	err error
}

func newLineWriter(out io.Writer) *lineWriter {
	return &lineWriter{out: out}
}

func (w *lineWriter) writeLine(line []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	_, err := w.out.Write(appendLine(line))
	if err != nil {
		w.err = err
	}
	return err
}

func (w *lineWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func appendLine(line []byte) []byte {
	out := make([]byte, 0, len(line)+1)
	out = append(out, line...)
	return append(out, '\n')
}
