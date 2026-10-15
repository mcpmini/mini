package main

import (
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// logFile takes Go's log output so it stays off the terminal; a clean run writes no file.
type logFile struct {
	path string
	mu   sync.Mutex
	file *os.File
	err  error
}

func newLogFile(configDir, name string) *logFile {
	return &logFile{path: filepath.Join(configDir, "internal", name)}
}

func (l *logFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil && l.err == nil {
		l.file, l.err = openLogFile(l.path)
	}
	if l.err != nil {
		// Nowhere to put it: dropping it beats mixing it into command output.
		return len(p), nil
	}
	return l.file.Write(p)
}

func openLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func (l *logFile) redirect() (restore func()) {
	previousLogger, previousOutput, previousFlags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(l, nil)))
	return func() {
		slog.SetDefault(previousLogger)
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		l.close()
	}
}

func (l *logFile) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close() //nolint:errcheck // every write already went through
	}
}

func (l *logFile) written() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.path, l.file != nil
}
