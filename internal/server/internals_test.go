//go:build test

package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

func TestSession_Conn_nilWhenEmpty(t *testing.T) {
	s := newSession(clock.NewFake())
	if conn := s.Conn("anyserver"); conn != nil {
		t.Errorf("expected nil for empty session, got %v", conn)
	}
}

func TestSession_GetOrSetConn_storesFirst(t *testing.T) {
	s := newSession(clock.NewFake())
	fake := &transport.FakeConnection{}
	got := s.GetOrSetConn("srv", fake)
	if got != fake {
		t.Error("expected stored connection to be returned")
	}
	if s.Conn("srv") != fake {
		t.Error("expected Conn to return stored connection")
	}
}

func TestSession_GetOrSetConn_returnsExistingOnRace(t *testing.T) {
	s := newSession(clock.NewFake())
	first := &transport.FakeConnection{}
	second := &transport.FakeConnection{}

	s.GetOrSetConn("srv", first)
	// second call: loser is closed, winner (first) is returned
	got := s.GetOrSetConn("srv", second)
	if got != first {
		t.Error("expected existing connection to win the race")
	}
	if !second.Closed {
		t.Error("expected losing connection to be closed")
	}
}

func TestSession_idleDuration_trueWhenOld(t *testing.T) {
	fakeClock := clock.NewFake()
	s := newSession(fakeClock)
	fakeClock.Advance(2 * time.Hour)
	if _, ok := s.idleDuration(fakeClock.Now().Add(-time.Hour)); !ok {
		t.Error("expected idleDuration to return true for future deadline")
	}
}

func TestSession_idleDuration_falseAfterTouch(t *testing.T) {
	fakeClock := clock.NewFake()
	s := newSession(fakeClock)
	s.touch()
	if _, ok := s.idleDuration(fakeClock.Now().Add(-time.Hour)); ok {
		t.Error("expected idleDuration to return false after recent touch")
	}
}

func TestSession_Close_closesConns(t *testing.T) {
	s := newSession(clock.NewFake())
	fake := &transport.FakeConnection{}
	s.GetOrSetConn("srv", fake)
	s.Close()
	if !fake.Closed {
		t.Error("expected connection to be closed after session Close")
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
	ch := s.enableNotifications()
	defer func() {
		s.disableNotifications()
		close(ch)
	}()
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

func TestNewSessionID_unique(t *testing.T) {
	a, b := transport.NewSessionID(), transport.NewSessionID()
	if a == "" || b == "" {
		t.Error("expected non-empty session IDs")
	}
	if a == b {
		t.Errorf("expected unique session IDs, got %q twice", a)
	}
}

func TestConnError_unwrap(t *testing.T) {
	inner := errors.New("inner error")
	ce := connError{err: inner}
	if ce.Error() != "inner error" {
		t.Errorf("Error() = %q, want %q", ce.Error(), "inner error")
	}
	if !errors.Is(ce, inner) {
		t.Error("expected errors.Is to find inner error via Unwrap")
	}
}

func TestIsSameHost_matching(t *testing.T) {
	r := &http.Request{Host: "127.0.0.1:4857"}
	cases := []struct {
		origin string
		want   bool
	}{
		{"http://127.0.0.1:4857", true},
		{"https://127.0.0.1:4857", true},
		{"http://evil.com", false},
		{"http://127.0.0.1:9999", false},
	}
	for _, c := range cases {
		if got := isSameHost(r, c.origin); got != c.want {
			t.Errorf("isSameHost(%q) = %v, want %v", c.origin, got, c.want)
		}
	}
}

func TestRunSessionEviction_evictsIdleSessions(t *testing.T) {
	fakeClock := clock.NewFake()
	cfg := config.DefaultConfig()
	cfg.ResponseDir = t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewWithConfigDir(cfg, t.TempDir(), logger, WithClock(fakeClock))

	srv.sessions.getOrCreate("old-session")
	fakeClock.Advance(2 * time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.RunSessionEviction(ctx, time.Hour)
	}()

	if err := fakeClock.BlockUntilContext(context.Background(), 2); err != nil {
		t.Fatal("ticker not registered:", err)
	}
	fakeClock.Advance(30 * time.Minute)

	// Poll for the eviction to complete, then cancel.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.sessions.count() == 0 {
			cancel()
			<-done
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("expected 0 sessions after eviction, got %d", srv.sessions.count())
}

