package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	clock    clock.Clock
}

func newSessionStore(clock clock.Clock) *sessionStore {
	return &sessionStore{sessions: make(map[string]*Session), clock: clock}
}

func sessionIDPrefix(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (st *sessionStore) getOrCreate(id string) *Session {
	st.mu.Lock()
	s, ok := st.getOrCreateLocked(id)
	s.touch()
	st.mu.Unlock()
	if !ok {
		slog.Debug("session created", "session_id", sessionIDPrefix(id))
	}
	return s
}

func (st *sessionStore) acquire(id string) (*Session, func()) {
	st.mu.Lock()
	s, _ := st.getOrCreateLocked(id)
	s.beginCall()
	st.mu.Unlock()
	return s, s.endCall
}

func (st *sessionStore) get(id string) *Session {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.sessions[id]
}

func (st *sessionStore) getOrCreateLocked(id string) (*Session, bool) {
	s, ok := st.sessions[id]
	if !ok {
		s = newSession(st.clock)
		st.sessions[id] = s
	}
	return s, ok
}

func (st *sessionStore) delete(id string) {
	st.mu.Lock()
	s := st.sessions[id]
	delete(st.sessions, id)
	st.mu.Unlock()
	if s != nil {
		s.Close()
	}
}

func (st *sessionStore) closeAll() {
	st.mu.Lock()
	sessions := st.sessions
	st.sessions = make(map[string]*Session)
	st.mu.Unlock()
	for _, session := range sessions {
		session.Close()
	}
}

func (st *sessionStore) count() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.sessions)
}

func (st *sessionStore) snapshotSessions() []*Session {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]*Session, 0, len(st.sessions))
	for _, s := range st.sessions {
		out = append(out, s)
	}
	return out
}

// closeServerConnections closes and removes per-session connections to the
// named server from all active sessions. Called when a server is removed at
// runtime so per-session connections don't linger until session eviction.
func (st *sessionStore) closeServerConnections(serverName string) {
	for _, s := range st.snapshotSessions() {
		s.RemoveConn(serverName)
	}
}

type sessionMetrics struct {
	calls, errors, latencyMs, tokensSaved int64
}

func sumSessionMetrics(sessions []*Session) sessionMetrics {
	var m sessionMetrics
	for _, s := range sessions {
		m.calls += s.totalCalls.Load()
		m.errors += s.totalErrors.Load()
		m.latencyMs += s.totalLatencyMs.Load()
		m.tokensSaved += s.estTokensSaved.Load()
	}
	return m
}

func (st *sessionStore) aggregateMetrics() map[string]any {
	sessions := st.snapshotSessions()
	sm := sumSessionMetrics(sessions)
	calls, errors, latencyMs, tokensSaved := sm.calls, sm.errors, sm.latencyMs, sm.tokensSaved
	m := map[string]any{
		"active":           len(sessions),
		"calls":            calls,
		"errors":           errors,
		"est_tokens_saved": tokensSaved,
	}
	if calls > 0 {
		m["avg_latency_ms"] = latencyMs / calls
	}
	return m
}

type evictedSession struct {
	session      *Session
	id           string
	idleDuration time.Duration
}

func (st *sessionStore) collectIdleSessions(deadline time.Time) []evictedSession {
	st.mu.Lock()
	defer st.mu.Unlock()
	var evicted []evictedSession
	for id, s := range st.sessions {
		if idleDur, ok := s.idleDuration(deadline); ok {
			evicted = append(evicted, evictedSession{s, id, idleDur})
			delete(st.sessions, id)
		}
	}
	return evicted
}

func (st *sessionStore) evictIdle(deadline time.Time) {
	for _, e := range st.collectIdleSessions(deadline) {
		slog.Info(
			"session evicted",
			"session_id",
			sessionIDPrefix(e.id),
			"idle_duration",
			e.idleDuration.Round(time.Second),
		)
		e.session.markAborted() // unblock any goroutine waiting in waitInitialized
		e.session.Close()
	}
}

// Must be called in a goroutine; blocks until ctx is canceled.
func (s *Server) RunSessionEviction(ctx context.Context, maxIdle time.Duration) {
	s.runSessionEviction(ctx, maxIdle, nil)
}

func (s *Server) runSessionEviction(ctx context.Context, maxIdle time.Duration, afterEvict func()) {
	ticker := s.clock.NewTicker(maxIdle / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.Chan():
			s.sessions.evictIdle(s.clock.Now().Add(-maxIdle))
			if afterEvict != nil {
				afterEvict()
			}
		case <-ctx.Done():
			return
		}
	}
}
