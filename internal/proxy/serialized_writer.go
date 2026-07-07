package proxy

import (
	"fmt"
	"io"
	"sync"
)

type serializedWriter struct {
	out io.Writer
	mu  sync.Mutex
}

func newSerializedWriter(out io.Writer) *serializedWriter {
	return &serializedWriter{out: out}
}

func (w *serializedWriter) writeLine(line []byte) {
	w.mu.Lock()
	fmt.Fprintf(w.out, "%s\n", line) //nolint:errcheck
	w.mu.Unlock()
}
