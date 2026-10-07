package main

import (
	"log"
	"os"
	"path/filepath"
	"sync"
)

// uiLogs holds what Go's log would print while the full-screen UI owns the terminal. The file
// is created only when something is logged, so a clean run leaves nothing behind.
type uiLogs struct {
	path string
	mu   sync.Mutex
	file *os.File
	err  error
}

func newUILogs(configDir string) *uiLogs {
	return &uiLogs{path: filepath.Join(configDir, "internal", "init.log")}
}

func (l *uiLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil && l.err == nil {
		l.file, l.err = openUILog(l.path)
	}
	if l.err != nil {
		// Nowhere to put it: dropping it beats drawing over the UI.
		return len(p), nil
	}
	return l.file.Write(p)
}

func openUILog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// redirect sends Go's log here until the returned restore runs.
func (l *uiLogs) redirect() (restore func()) {
	previous := log.Writer()
	log.SetOutput(l)
	return func() {
		log.SetOutput(previous)
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.file != nil {
			l.file.Close() //nolint:errcheck // every write already went through
		}
	}
}

// written is the log file's path when the UI logged anything, for the summary to name.
func (l *uiLogs) written() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.path, l.file != nil
}
