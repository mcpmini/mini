package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

type notificationStream struct {
	daemon DaemonSession
	writer *serializedWriter
	clock  clock.Clock

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}

	mu      sync.Mutex
	bound   bool
	desired linkState
}

type activeNotificationStream struct {
	state  linkState
	cancel context.CancelFunc
	done   chan struct{}
}

func newNotificationStream(daemon DaemonSession, writer *serializedWriter, clk clock.Clock) *notificationStream {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &notificationStream{
		daemon: daemon,
		writer: writer,
		clock:  clk,
		ctx:    ctx,
		cancel: cancel,
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	go stream.run()
	return stream
}

func (s *notificationStream) Bind(state linkState) {
	s.mu.Lock()
	if s.bound && state.generation < s.desired.generation {
		s.mu.Unlock()
		return
	}
	s.bound = true
	s.desired = state
	s.mu.Unlock()
	s.signal()
}

func (s *notificationStream) Close() {
	s.cancel()
	<-s.done
}

func (s *notificationStream) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *notificationStream) run() {
	defer close(s.done)
	var active *activeNotificationStream
	for {
		next, ok := s.next(active)
		if !ok {
			return
		}
		if next == nil {
			continue
		}
		active, ok = s.wait(next)
		if !ok {
			return
		}
	}
}

func (s *notificationStream) next(active *activeNotificationStream) (*activeNotificationStream, bool) {
	if active != nil {
		return active, true
	}
	state, ok := s.desiredState()
	if ok {
		return s.start(state), true
	}
	return nil, s.waitForBind()
}

func (s *notificationStream) wait(active *activeNotificationStream) (*activeNotificationStream, bool) {
	select {
	case <-s.ctx.Done():
		active.stop()
		return nil, false
	case <-s.wake:
		state, ok := s.desiredState()
		if ok && state != active.state {
			active.stop()
			return nil, true
		}
		return active, true
	case <-active.done:
		active.cancel()
		if !s.retryDelay() {
			return nil, false
		}
		return nil, true
	}
}

func (s *notificationStream) start(state linkState) *activeNotificationStream {
	ctx, cancel := context.WithCancel(s.ctx)
	active := &activeNotificationStream{state: state, cancel: cancel, done: make(chan struct{})}
	go s.consume(ctx, state, active.done)
	return active
}

func (s *notificationStream) desiredState() (linkState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.desired, s.bound
}

func (s *notificationStream) waitForBind() bool {
	select {
	case <-s.ctx.Done():
		return false
	case <-s.wake:
		return true
	}
}

func (s *notificationStream) retryDelay() bool {
	timer := s.clock.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return false
	case <-s.wake:
		return true
	case <-timer.Chan():
		return true
	}
}

func (s *notificationStream) consume(ctx context.Context, state linkState, done chan struct{}) {
	defer close(done)
	req, err := newDaemonStreamRequest(ctx, s.daemon, state.token)
	if err != nil {
		return
	}
	resp, err := s.daemon.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return
	}
	s.scan(resp.Body)
}

func newDaemonStreamRequest(ctx context.Context, daemon DaemonSession, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", daemon.sessionID)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func (s *notificationStream) scan(body io.Reader) {
	_ = transport.ScanSSEMessages(body, func(message json.RawMessage) error {
		s.forward(message)
		return nil
	})
}

func (s *notificationStream) forward(message json.RawMessage) {
	if !json.Valid(message) {
		return
	}
	s.writer.writeLine(message)
}

func (s *activeNotificationStream) stop() {
	s.cancel()
	<-s.done
}
