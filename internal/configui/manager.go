// Package configui serves the browser page where a user reviews a server's tools after logging in.
// It runs in the daemon, on a loopback port of its own, and reaches servers only through Backend.
package configui

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

// Manager binds the page's loopback port only while a link or session is live, so a page nobody
// has open leaves nothing listening.
type Manager struct {
	clock   clock.Clock
	backend Backend

	mu       sync.Mutex
	entries  map[[sha256.Size]byte]entry
	sessions map[[sha256.Size]byte]*session
	listener *uiListener
}

type uiListener struct {
	srv    *http.Server
	host   string
	closed chan struct{}
}

func NewManager(backend Backend, c clock.Clock) *Manager {
	return &Manager{
		clock:    c,
		backend:  backend,
		entries:  map[[sha256.Size]byte]entry{},
		sessions: map[[sha256.Size]byte]*session{},
	}
}

// Open returns a one-time link to the page for servers, or "" when no port can be bound.
func (m *Manager) Open(servers []string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listener == nil {
		l, err := m.listen()
		if err != nil {
			slog.Warn("config page unavailable", "err", err)
			return ""
		}
		m.listener = l
	}
	token := newSecret()
	m.entries[secretKey(token)] = entry{servers: slices.Clone(servers), expires: m.clock.Now().Add(entryTTL)}
	return "http://" + m.listener.host + "/#t=" + token
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.entries)
	clear(m.sessions)
	m.closeListener()
}

func (m *Manager) listen() (*uiListener, error) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	l := &uiListener{host: ln.Addr().String(), closed: make(chan struct{})}
	l.srv = &http.Server{Handler: m.handler(l.host), ReadHeaderTimeout: 5 * time.Second}
	go l.srv.Serve(ln) //nolint:errcheck // Serve only returns once the listener is closed
	go m.expireUntilClosed(l)
	return l, nil
}

func (m *Manager) expireUntilClosed(l *uiListener) {
	ticker := m.clock.NewTicker(janitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.closed:
			return
		case <-ticker.Chan():
			m.expire(l)
		}
	}
}

func (m *Manager) expire(l *uiListener) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	maps.DeleteFunc(m.entries, func(_ [sha256.Size]byte, e entry) bool { return !now.Before(e.expires) })
	maps.DeleteFunc(m.sessions, func(_ [sha256.Size]byte, s *session) bool { return !now.Before(s.expiresAt()) })
	if m.listener == l && len(m.entries) == 0 && len(m.sessions) == 0 {
		m.closeListener()
	}
}

func (m *Manager) closeListener() {
	if m.listener == nil {
		return
	}
	m.listener.srv.Close() //nolint:errcheck // closing drops the page's connections; there is nothing to retry
	close(m.listener.closed)
	m.listener = nil
}

// redeem spends a link's token once and starts a session for its servers.
func (m *Manager) redeem(token string) (bearer string, s session, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := secretKey(token)
	e, ok := m.entries[key]
	delete(m.entries, key)
	now := m.clock.Now()
	if !ok || !now.Before(e.expires) {
		return "", session{}, false
	}
	bearer, s = newSecret(), session{servers: e.servers, created: now, lastActive: now}
	m.sessions[secretKey(bearer)] = &s
	return bearer, s, true
}

// liveSession returns a copy of the bearer's session, after refreshing its idle timer when touch is set.
func (m *Manager) liveSession(bearer string, touch bool) (session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[secretKey(bearer)]
	now := m.clock.Now()
	if !ok || !now.Before(s.expiresAt()) {
		return session{}, false
	}
	if touch {
		s.lastActive = now
	}
	return *s, true
}
