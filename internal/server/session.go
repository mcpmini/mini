package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type Session struct {
	mu            sync.RWMutex
	projections   map[string]*config.ProjectionConfig
	conns         map[string]transport.Connection
	lastUsed      time.Time
	activeCalls   int
	clock         clock.Clock
	notifications sessionNotifications
	dialMu        sync.Mutex
	dialMap       map[string]*dialOnce
	// inFlight tracks cancellable in-progress requests keyed by raw JSON request ID.
	// Used to honor notifications/cancelled from agents.
	inFlightMu sync.Mutex
	inFlight   map[string]context.CancelFunc

	// initDone / initAbort implement the initialization gate.
	// Non-ping requests block in waitInitialized until one fires:
	//   initDone  → initialize completed successfully, proceed
	//   initAbort → connection closed before initialize arrived, return error
	// Spec: "The initialization phase MUST be the first interaction between client and server."
	// https://github.com/modelcontextprotocol/modelcontextprotocol/blob/459f1355af9ab1eec00bfa8124d10d4f1d0ab09c/docs/specification/2025-03-26/basic/lifecycle.mdx#L38
	initDone      chan struct{}
	initAbort     chan struct{}
	initOnce      sync.Once
	initAbortOnce sync.Once

	mode           atomic.Int32 // holds ToolMode; zero value = ToolModeProxy
	modeOnce       sync.Once
	clientReady    atomic.Bool
	totalCalls     atomic.Int64
	totalErrors    atomic.Int64
	totalLatencyMs atomic.Int64
	estTokensSaved atomic.Int64
}

// dialOnce coordinates at most one in-flight dial per (server, session) pair.
type dialOnce struct {
	mu   sync.Mutex
	conn transport.Connection
	err  error
	done bool
}

func (s *Session) dialOnceFor(serverName string, dial func() (transport.Connection, error)) (transport.Connection, error) {
	d := s.getDialOnce(serverName)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.done {
		return d.conn, d.err
	}
	d.conn, d.err = dial()
	if d.err == nil {
		d.done = true
	}
	return d.conn, d.err
}

func (s *Session) getDialOnce(serverName string) *dialOnce {
	s.dialMu.Lock()
	defer s.dialMu.Unlock()
	if d := s.dialMap[serverName]; d != nil {
		return d
	}
	d := &dialOnce{}
	s.dialMap[serverName] = d
	return d
}

func newSession(clock clock.Clock) *Session {
	return &Session{
		projections:   make(map[string]*config.ProjectionConfig),
		conns:         make(map[string]transport.Connection),
		dialMap:       make(map[string]*dialOnce),
		inFlight:      make(map[string]context.CancelFunc),
		lastUsed:      clock.Now(),
		clock:         clock,
		initDone:      make(chan struct{}),
		initAbort:     make(chan struct{}),
		notifications: newSessionNotifications(),
	}
}

func (s *Session) toolMode() transport.ToolMode {
	return transport.ToolMode(s.mode.Load())
}

func (s *Session) setToolModeOnce(m transport.ToolMode) {
	s.modeOnce.Do(func() { s.mode.Store(int32(m)) })
}

func (s *Session) markInitialized() {
	s.initOnce.Do(func() {
		close(s.initDone)
		slog.Debug("session initialized")
	})
}

func (s *Session) markClientReady() {
	select {
	case <-s.initDone:
		s.clientReady.Store(true)
	default:
	}
}

// markAborted is called when the Serve loop ends without initialization completing.
// It unblocks waiting goroutines so they can return an error and let Serve exit.
func (s *Session) markAborted() {
	s.initAbortOnce.Do(func() { close(s.initAbort) })
}

// waitInitialized blocks until initialization succeeds, the serving loop ends,
// or the request context is cancelled. Returns true only if initialized.
func (s *Session) waitInitialized(ctx context.Context) bool {
	select {
	case <-s.initDone:
		return true
	default:
	}
	select {
	case <-s.initDone:
		return true
	case <-s.initAbort:
		return false
	case <-ctx.Done():
		return false
	}
}

// registerInFlight registers a cancellable context for the request with the
// given raw-JSON ID. The ID is stored as its raw JSON string so that numeric
// IDs (5) and string IDs ("5") are correctly distinguished.
func (s *Session) registerInFlight(rawID json.RawMessage, cancel context.CancelFunc) {
	s.inFlightMu.Lock()
	s.inFlight[string(rawID)] = cancel
	s.inFlightMu.Unlock()
}

func (s *Session) removeInFlight(rawID json.RawMessage) {
	s.inFlightMu.Lock()
	delete(s.inFlight, string(rawID))
	s.inFlightMu.Unlock()
}

// cancelInFlight cancels the in-flight request matching rawID, if any.
// Safe to call if the request has already completed.
func (s *Session) cancelInFlight(rawID json.RawMessage) {
	s.inFlightMu.Lock()
	cancel := s.inFlight[string(rawID)]
	s.inFlightMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Session) SetProjection(toolFullName string, p *config.ProjectionConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projections[toolFullName] = p
	s.lastUsed = s.clock.Now()
}

func (s *Session) Projection(toolFullName string) *config.ProjectionConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.projections[toolFullName]
}

func (s *Session) Conn(serverName string) transport.Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conns[serverName]
}

// GetOrSetConn atomically stores conn for serverName if no connection exists yet.
// If a connection was already set (by a concurrent goroutine), conn is closed and
// the existing connection is returned. This prevents duplicate dial races.
func (s *Session) GetOrSetConn(serverName string, conn transport.Connection) transport.Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.conns[serverName]; existing != nil {
		conn.Close()
		return existing
	}
	s.conns[serverName] = conn
	return conn
}

func (s *Session) RemoveConn(serverName string) {
	s.mu.Lock()
	conn := s.conns[serverName]
	delete(s.conns, serverName)
	s.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
	// Also clear the dialOnce so the next call attempts a fresh dial rather
	// than reusing the now-broken connection stored in the old dialOnce entry.
	s.dialMu.Lock()
	delete(s.dialMap, serverName)
	s.dialMu.Unlock()
}

// EvictConn removes serverName from the session map only if the stored conn
// matches the provided conn (identity check). It does NOT close the conn —
// callers that own the conn must close it themselves. This prevents a
// concurrent goroutine's RemoveConn from closing a conn another goroutine
// is still using.
func (s *Session) EvictConn(serverName string, conn transport.Connection) {
	s.mu.Lock()
	if s.conns[serverName] == conn {
		delete(s.conns, serverName)
	}
	s.mu.Unlock()
	s.dialMu.Lock()
	delete(s.dialMap, serverName)
	s.dialMu.Unlock()
}

func (s *Session) Close() {
	s.mu.Lock()
	for _, conn := range s.conns {
		conn.Close()
	}
	s.conns = make(map[string]transport.Connection)
	s.mu.Unlock()
	s.closeNotificationStreams()
	slog.Debug("session closed")
}

func (s *Session) touch() {
	s.mu.Lock()
	s.lastUsed = s.clock.Now()
	s.mu.Unlock()
}

func (s *Session) beginCall() {
	s.mu.Lock()
	s.activeCalls++
	s.lastUsed = s.clock.Now()
	s.mu.Unlock()
}

func (s *Session) endCall() {
	s.mu.Lock()
	s.activeCalls--
	s.lastUsed = s.clock.Now()
	s.mu.Unlock()
}

func (s *Session) recordCall(latencyMs, tokensSaved int64, isErr bool) {
	s.totalCalls.Add(1)
	s.totalLatencyMs.Add(latencyMs)
	s.estTokensSaved.Add(tokensSaved)
	if isErr {
		s.totalErrors.Add(1)
	}
}

func (s *Session) idleDuration(deadline time.Time) (time.Duration, bool) {
	if s.notifications.hasOpenStreams() {
		return 0, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.activeCalls != 0 {
		return 0, false
	}
	if !s.lastUsed.Before(deadline) {
		return 0, false
	}
	return s.clock.Since(s.lastUsed), true
}

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

func (st *sessionStore) getOrCreateLocked(id string) (*Session, bool) {
	s, ok := st.sessions[id]
	if !ok {
		s = newSession(st.clock)
		st.sessions[id] = s
	}
	return s, ok
}

func (st *sessionStore) get(id string) *Session {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.sessions[id]
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

func (st *sessionStore) closeNotificationStreams() {
	for _, session := range st.snapshotSessions() {
		session.closeNotificationStreams()
	}
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
		slog.Info("session evicted", "session_id", sessionIDPrefix(e.id), "idle_duration", e.idleDuration.Round(time.Second))
		e.session.markAborted() // unblock any goroutine waiting in waitInitialized
		e.session.Close()
	}
}
