//go:build test

package server

import (
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestSessionStore_evictIdle_removesSession(t *testing.T) {
	fc := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	st := newSessionStore(fc)
	st.getOrCreate("abcdefghijklmnop")
	fc.Advance(2 * time.Hour)
	st.evictIdle(fc.Now().Add(-time.Hour))

	if st.count() != 0 {
		t.Errorf("expected 0 sessions after eviction, got %d", st.count())
	}
}

func TestSessionStore_evictIdle_keepsActiveSession(t *testing.T) {
	fc := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	st := newSessionStore(fc)
	st.getOrCreate("abcdefghijklmnop")
	st.evictIdle(fc.Now().Add(-time.Hour))

	if st.count() != 1 {
		t.Errorf("expected 1 session (recently used), got %d", st.count())
	}
}

func TestSessionStore_getOrCreate_returnsSameSession(t *testing.T) {
	st := newSessionStore(clock.System())
	s1 := st.getOrCreate("abcdefghijklmnop")
	s2 := st.getOrCreate("abcdefghijklmnop")
	if s1 != s2 {
		t.Error("expected same session for same ID")
	}
}

func TestSessionStore_getOrCreate_separateSessions(t *testing.T) {
	st := newSessionStore(clock.System())
	s1 := st.getOrCreate("aaaaaaaaaaaaaaaa")
	s2 := st.getOrCreate("bbbbbbbbbbbbbbbb")
	if s1 == s2 {
		t.Error("expected distinct sessions for distinct IDs")
	}
	if st.count() != 2 {
		t.Errorf("expected 2 sessions, got %d", st.count())
	}
}
