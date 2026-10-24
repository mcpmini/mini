package server

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/transport"
)

type Session struct {
	mu           sync.RWMutex
	projections  map[string]*config.ProjectionConfig
	conns        map[string]transport.Connection
	lastUsed     time.Time
	activeCalls  int
	clock        clock.Clock
	toolsChanged toolsChangedOutbox
	dialMu       sync.Mutex
	dialMap      map[string]*dialOnce
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

	clientReady atomic.Bool
	// HTTP sessions keep the initialization gate even for modern requests: the stdio proxy runs the
	// handshake for its modern agents and learns from "not initialized" that the daemon lost it.
	httpSession    atomic.Bool
	modern         atomic.Bool
	listensMu      sync.Mutex
	listens        map[string]context.CancelFunc
	listenTools    map[string]bool
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

func (s *Session) dialOnceFor(
	serverName string,
	dial func() (transport.Connection, error),
) (transport.Connection, error) {
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
		projections:  make(map[string]*config.ProjectionConfig),
		conns:        make(map[string]transport.Connection),
		dialMap:      make(map[string]*dialOnce),
		inFlight:     make(map[string]context.CancelFunc),
		listens:      make(map[string]context.CancelFunc),
		listenTools:  make(map[string]bool),
		lastUsed:     clock.Now(),
		clock:        clock,
		initDone:     make(chan struct{}),
		initAbort:    make(chan struct{}),
		toolsChanged: newToolsChangedOutbox(),
	}
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
	s.cancelListens()
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
		conn.Close() //nolint:errcheck // The existing connection wins; Close only releases the redundant dial result.
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
		conn.Close() //nolint:errcheck // The connection is removed; Close only releases the removed session resource.
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
	s.cancelListens()
	s.mu.Lock()
	for _, conn := range s.conns {
		conn.Close() //nolint:errcheck // Session shutdown has no caller to receive connection cleanup errors.
	}
	s.conns = make(map[string]transport.Connection)
	s.mu.Unlock()
	s.closeToolsChangedStreams()
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
	if s.toolsChanged.hasOpenStream() {
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
