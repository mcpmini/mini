//go:build test

package server

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestSessionStore_evictIdle_removesSession(t *testing.T) {
	fakeClock := clock.NewFake()
	st := newSessionStore(fakeClock)
	st.getOrCreate("abcdefghijklmnop")
	fakeClock.Advance(2 * time.Hour)
	st.evictIdle(fakeClock.Now().Add(-time.Hour))

	if st.count() != 0 {
		t.Errorf("expected 0 sessions after eviction, got %d", st.count())
	}
}

func TestSessionStore_evictIdle_keepsActiveSession(t *testing.T) {
	fakeClock := clock.NewFake()
	st := newSessionStore(fakeClock)
	st.getOrCreate("abcdefghijklmnop")
	st.evictIdle(fakeClock.Now().Add(-time.Hour))

	if st.count() != 1 {
		t.Errorf("expected 1 session (recently used), got %d", st.count())
	}
}

func TestSessionStore_evictIdle_keepsInFlightRequest(t *testing.T) {
	fakeClock := clock.NewFake()
	st := newSessionStore(fakeClock)
	s, release := st.acquire("abcdefghijklmnop")
	fakeClock.Advance(2 * time.Hour)
	st.evictIdle(fakeClock.Now().Add(-time.Hour))
	if got := st.getOrCreate("abcdefghijklmnop"); got != s {
		t.Fatal("in-flight session was evicted")
	}
	release()
	fakeClock.Advance(2 * time.Hour)
	st.evictIdle(fakeClock.Now().Add(-time.Hour))
	if got := st.getOrCreate("abcdefghijklmnop"); got == s {
		t.Fatal("completed idle session was not evicted")
	}
}

func TestSessionStore_getOrCreate_returnsSameSession(t *testing.T) {
	st := newSessionStore(clock.NewFake())
	s1 := st.getOrCreate("abcdefghijklmnop")
	s2 := st.getOrCreate("abcdefghijklmnop")
	if s1 != s2 {
		t.Error("expected same session for same ID")
	}
}

func TestSessionStore_getOrCreate_separateSessions(t *testing.T) {
	st := newSessionStore(clock.NewFake())
	s1 := st.getOrCreate("aaaaaaaaaaaaaaaa")
	s2 := st.getOrCreate("bbbbbbbbbbbbbbbb")
	if s1 == s2 {
		t.Error("expected distinct sessions for distinct IDs")
	}
	if st.count() != 2 {
		t.Errorf("expected 2 sessions, got %d", st.count())
	}
}

func TestSessionStore_evictIdle_removesOldSessions(t *testing.T) {
	fakeClock := clock.NewFake()
	st := newSessionStore(fakeClock)
	st.getOrCreate("old")
	fakeClock.Advance(2 * time.Hour)
	st.getOrCreate("fresh")

	st.evictIdle(fakeClock.Now().Add(-time.Hour))

	if st.count() != 1 {
		t.Errorf("expected 1 session after eviction, got %d", st.count())
	}
}

func TestSessionStore_evictIdle_closesEvictedConns(t *testing.T) {
	fakeClock := clock.NewFake()
	st := newSessionStore(fakeClock)
	s := st.getOrCreate("stale")
	fake := &transport.FakeConnection{}
	s.GetOrSetConn("srv", fake)
	fakeClock.Advance(2 * time.Hour)

	st.evictIdle(fakeClock.Now().Add(-time.Hour))

	if !fake.Closed {
		t.Error("expected evicted session's connections to be closed")
	}
}

func TestSessionStore_evictIdle_keepsActiveNotificationSession(t *testing.T) {
	fakeClock := clock.NewFake()
	st := newSessionStore(fakeClock)
	s := st.getOrCreate("stdio")
	ch, ok := s.openToolsChangedStream()
	if !ok {
		t.Fatal("expected notification stream to open")
	}
	defer s.closeToolsChangedStream(ch)
	fakeClock.Advance(2 * time.Hour)

	st.evictIdle(fakeClock.Now().Add(-time.Hour))

	if st.count() != 1 {
		t.Fatalf("expected active stdio session to be preserved, got %d sessions", st.count())
	}
}

func TestSessionStore_evictIdle_unblocksPendingWaiters(t *testing.T) {
	fakeClock := clock.NewFake()
	st := newSessionStore(fakeClock)
	s := st.getOrCreate("stale")
	fakeClock.Advance(2 * time.Hour)

	unblocked := make(chan bool, 1)
	go func() {
		unblocked <- s.waitInitialized(context.Background())
	}()

	st.evictIdle(fakeClock.Now().Add(-time.Hour))

	select {
	case got := <-unblocked:
		if got {
			t.Error("waitInitialized should return false for evicted session")
		}
	case <-time.After(time.Second):
		t.Fatal("waitInitialized blocked after session eviction")
	}
}

func TestRunSessionEviction_evictsIdleSessions(t *testing.T) {
	fakeClock := clock.NewFake()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(Params{Config: cfg, ConfigDir: t.TempDir(), Logger: logger, Clock: fakeClock})

	srv.sessions.getOrCreate("old-session")
	fakeClock.Advance(2 * time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	evicted := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.runSessionEviction(ctx, time.Hour, func() { evicted <- struct{}{} })
	}()

	if err := fakeClock.BlockUntilContext(context.Background(), 2); err != nil {
		t.Fatal("ticker not registered:", err)
	}
	fakeClock.Advance(30 * time.Minute)

	select {
	case <-evicted:
	case <-t.Context().Done():
		t.Fatal("eviction did not complete")
	}
	cancel()
	<-done
	if got := srv.sessions.count(); got != 0 {
		t.Errorf("expected 0 sessions after eviction, got %d", got)
	}
}
