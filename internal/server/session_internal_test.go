//go:build test

package server

import (
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
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
