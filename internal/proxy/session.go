package proxy

import (
	"sync/atomic"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

type proxySession struct {
	daemon             DaemonSession
	resolver           *DaemonResolver
	link               *daemonLink
	toolMode           transport.ToolMode
	clock              clock.Clock
	writer             *lineWriter
	notificationStream *notificationStream
	initialized        atomic.Bool
	clientReady        atomic.Bool
}

func newProxySession(p RunParams, writer *lineWriter) *proxySession {
	session := DaemonSession{client: p.Client, sessionID: p.SessionID}
	var notifications *notificationStream
	if p.ToolMode == transport.ToolModeProxy {
		notifications = newNotificationStream(session, writer, p.Clock)
	}
	return &proxySession{
		daemon:             session,
		resolver:           p.Resolver,
		link:               newDaemonLink(p.Token),
		toolMode:           p.ToolMode,
		clock:              p.Clock,
		writer:             writer,
		notificationStream: notifications,
	}
}

func (s *proxySession) close() {
	if s.notificationStream != nil {
		s.notificationStream.Close()
	}
}

func (s *proxySession) writeLine(line []byte) error {
	return s.writer.writeLine(line)
}

func (s *proxySession) forward(message forwardedMessage) []byte {
	state := s.link.snapshot()
	var out forwardOutcome
	for attempt := range maxRecoveryAttempts {
		out = classifyForward(s.daemonAt(state), message.line)
		if out.kind == outcomeOK || out.kind == outcomeOther {
			break
		}
		next, ok := s.recoverForwarding(state, out.kind, message.kind)
		if !ok || attempt+1 == maxRecoveryAttempts {
			break
		}
		state = next
		<-s.clock.NewTimer(jitteredBackoff(attempt)).Chan()
	}
	s.observeSuccessfulLifecycle(state, message.kind, out.kind)
	return out.resp
}

func (s *proxySession) daemonAt(state linkState) DaemonSession {
	daemon := s.daemon
	daemon.token = state.token
	return daemon
}

func (s *proxySession) observeSuccessfulLifecycle(state linkState, message forwardedMessageKind, kind outcomeKind) {
	if kind != outcomeOK {
		return
	}
	if message == forwardedMessageInitialize {
		s.initialized.Store(true)
	}
	if message == forwardedMessageInitialized && s.initialized.Load() {
		s.clientReady.Store(true)
		s.bindNotifications(state)
	}
}

func (s *proxySession) recoverForwarding(state linkState, kind outcomeKind, message forwardedMessageKind) (linkState, bool) {
	if kind == outcomeNotInitialized && message == forwardedMessageInitialize {
		return state, false
	}
	next, ok := s.recoverDaemonLink(state, kind)
	if !ok {
		return state, false
	}
	if message != forwardedMessageInitialize {
		s.daemonAt(next).Handshake(s.toolMode)
		s.restoreLifecycle(next)
	}
	return next, true
}

func (s *proxySession) recoverDaemonLink(state linkState, kind outcomeKind) (linkState, bool) {
	if kind != outcomeTransportDown && kind != outcomeUnauthorized {
		return state, true
	}
	next, err := s.link.recover(state.generation, s.resolver)
	if err != nil || next.generation == state.generation {
		return state, false
	}
	return next, true
}

func (s *proxySession) restoreLifecycle(state linkState) {
	s.initialized.Store(true)
	if s.clientReady.Load() {
		s.bindNotifications(state)
	}
}

func (s *proxySession) bindNotifications(state linkState) {
	if s.notificationStream != nil {
		s.notificationStream.Bind(state)
	}
}
