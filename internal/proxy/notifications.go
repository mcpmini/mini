package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

type notificationBridge struct {
	session DaemonSession
	out     io.Writer
	writeMu *sync.Mutex
	clock   clock.Clock

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}

	mu      sync.Mutex
	armed   bool
	desired linkState
}

type bridgeStream struct {
	state  linkState
	cancel context.CancelFunc
	done   chan struct{}
}

func newNotificationBridge(session DaemonSession, out io.Writer, writeMu *sync.Mutex, clk clock.Clock) *notificationBridge {
	ctx, cancel := context.WithCancel(context.Background())
	b := &notificationBridge{
		session: session,
		out:     out,
		writeMu: writeMu,
		clock:   clk,
		ctx:     ctx,
		cancel:  cancel,
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	go b.run()
	return b
}

func (b *notificationBridge) Arm(state linkState) {
	b.mu.Lock()
	b.armed = true
	b.desired = state
	b.mu.Unlock()
	b.signal()
}

func (b *notificationBridge) Close() {
	b.cancel()
	<-b.done
}

func (b *notificationBridge) signal() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *notificationBridge) run() {
	defer close(b.done)
	var stream *bridgeStream
	for {
		next, ok := b.nextStream(stream)
		if !ok {
			return
		}
		if next == nil {
			continue
		}
		stream, ok = b.waitOnStream(next)
		if !ok {
			return
		}
	}
}

func (b *notificationBridge) nextStream(stream *bridgeStream) (*bridgeStream, bool) {
	if stream != nil {
		return stream, true
	}
	state, ok := b.snapshotDesired()
	if ok {
		return b.startStream(state), true
	}
	return nil, b.waitForWake()
}

func (b *notificationBridge) waitOnStream(stream *bridgeStream) (*bridgeStream, bool) {
	select {
	case <-b.ctx.Done():
		stream.stop()
		return nil, false
	case <-b.wake:
		state, ok := b.snapshotDesired()
		if ok && state != stream.state {
			stream.stop()
			return nil, true
		}
		return stream, true
	case <-stream.done:
		stream.cancel()
		if !b.sleep() {
			return nil, false
		}
		return nil, true
	}
}

func (b *notificationBridge) startStream(state linkState) *bridgeStream {
	streamCtx, cancel := context.WithCancel(b.ctx)
	stream := &bridgeStream{state: state, cancel: cancel, done: make(chan struct{})}
	go b.consume(streamCtx, state, stream.done)
	return stream
}

func (s *bridgeStream) stop() {
	s.cancel()
	<-s.done
}

func (b *notificationBridge) snapshotDesired() (linkState, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.desired, b.armed
}

func (b *notificationBridge) waitForWake() bool {
	select {
	case <-b.ctx.Done():
		return false
	case <-b.wake:
		return true
	}
}

func (b *notificationBridge) sleep() bool {
	timer := b.clock.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-b.ctx.Done():
		return false
	case <-b.wake:
		return true
	case <-timer.Chan():
		return true
	}
}

func (b *notificationBridge) consume(ctx context.Context, state linkState, done chan struct{}) {
	defer close(done)
	req, err := newDaemonStreamRequest(ctx, b.session, state.token)
	if err != nil {
		return
	}
	resp, err := b.session.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return
	}
	b.scanSSE(resp.Body)
}

func newDaemonStreamRequest(ctx context.Context, session DaemonSession, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, daemonURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", session.sessionID)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func (b *notificationBridge) scanSSE(body io.Reader) {
	_ = transport.ScanSSEMessages(body, func(message json.RawMessage) error {
		b.forwardMessage(message)
		return nil
	})
}

func (b *notificationBridge) forwardMessage(message json.RawMessage) {
	if !json.Valid(message) {
		return
	}
	b.writeMu.Lock()
	fmt.Fprintf(b.out, "%s\n", message) //nolint:errcheck
	b.writeMu.Unlock()
}
