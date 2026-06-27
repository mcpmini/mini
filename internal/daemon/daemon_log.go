package daemon

import (
	"io"
	"os"
	"sync"
)

const maxDaemonLogBytes = 10 << 20 // 10MB

type cappedLog struct {
	mu      sync.Mutex
	f       *os.File
	path    string
	written int64
	cap     int64
}

// nopWriteCloser wraps a writer with a no-op Close, used as Stderr fallback
// so callers can safely defer Close() without closing os.Stderr.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// OpenCappedLog opens path for append-writing, rotating to path+".old" when
// the file exceeds maxDaemonLogBytes. Falls back to os.Stderr on open failure.
func OpenCappedLog(path string) io.WriteCloser {
	return openCappedLog(path, maxDaemonLogBytes)
}

func openCappedLog(path string, cap int64) io.WriteCloser {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nopWriteCloser{os.Stderr}
	}
	w := &cappedLog{f: f, path: path, cap: cap}
	if info, err := f.Stat(); err == nil {
		w.written = info.Size()
	}
	return w
}

func (c *cappedLog) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.written+int64(len(p)) > c.cap {
		c.rotate()
	}
	n, err := c.f.Write(p)
	c.written += int64(n)
	return n, err
}

func (c *cappedLog) rotate() {
	if c.f == os.Stderr {
		// closing os.Stderr silences the daemon — reset counter so writes don't re-trigger rotation
		c.written = 0
		return
	}
	c.f.Close()
	os.Rename(c.path, c.path+".old") //nolint:errcheck — rotation is best-effort; if rename fails the .old file is overwritten
	f, err := os.OpenFile(c.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		f = os.Stderr // last resort: don't crash if log dir becomes unwritable
	}
	c.f = f
	c.written = 0
}

func (c *cappedLog) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f == os.Stderr {
		return nil // don't close the process's own stderr
	}
	return c.f.Close()
}
