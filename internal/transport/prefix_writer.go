package transport

import (
	"bytes"
	"log/slog"
)

// prefixWriter captures subprocess stderr, splits on newlines, and logs each
// line with a server-identifying prefix. Partial lines are buffered until
// a newline arrives.
type prefixWriter struct {
	logger *slog.Logger
	prefix string
	buf    []byte
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		w.logger.Debug(w.prefix + string(w.buf[:idx]))
		w.buf = w.buf[idx+1:]
	}
	return len(p), nil
}
