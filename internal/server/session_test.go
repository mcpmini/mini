//go:build test

package server

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func withDebugLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func TestSessionStore_evictIdle_logsEviction(t *testing.T) {
	buf := withDebugLogger(t)
	st := newSessionStore()
	s := st.getOrCreate("abcdefghijklmnop")
	s.mu.Lock()
	s.lastUsed = time.Now().Add(-2 * time.Hour)
	s.mu.Unlock()

	st.evictIdle(time.Now().Add(-time.Hour))

	out := buf.String()
	if !strings.Contains(out, "session evicted") {
		t.Errorf("expected 'session evicted' in log output, got: %q", out)
	}
	if !strings.Contains(out, "abcdefgh") {
		t.Errorf("expected 8-char session ID prefix in log output, got: %q", out)
	}
}

func TestSessionStore_getOrCreate_logsNewSession(t *testing.T) {
	buf := withDebugLogger(t)
	st := newSessionStore()

	st.getOrCreate("abcdefghijklmnop")

	out := buf.String()
	if !strings.Contains(out, "session created") {
		t.Errorf("expected 'session created' in log output, got: %q", out)
	}
	if !strings.Contains(out, "abcdefgh") {
		t.Errorf("expected 8-char session ID prefix in log output, got: %q", out)
	}
}

func TestSessionStore_getOrCreate_noLogForExistingSession(t *testing.T) {
	st := newSessionStore()
	st.getOrCreate("abcdefghijklmnop")

	buf := withDebugLogger(t)
	st.getOrCreate("abcdefghijklmnop")

	out := buf.String()
	if strings.Contains(out, "session created") {
		t.Errorf("expected no 'session created' log for existing session, got: %q", out)
	}
}
