//go:build test

package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
	"github.com/mcpmini/mini/internal/transport"
)

func TestNotificationStream_modernTaggingAndLegacyForwarding(t *testing.T) {
	session, out := newTestProxySession(t, transport.ToolModeProxy)
	notification := toolsChangedLine(t)
	if err := session.notificationStream.forward(notification); err != nil {
		t.Fatal(err)
	}
	if out.String() != string(notification)+"\n" {
		t.Fatalf("legacy notification output = %q", out.String())
	}
	out.Reset()
	session.subs.modern.Store(true)
	if err := session.notificationStream.forward(notification); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("modern notification without listen was forwarded: %q", out.String())
	}
	listen := transport.NewSubscriptionAck(json.RawMessage(`9`), transport.ListenNotifications{ToolsListChanged: true})
	if err := session.subs.acknowledge(listen, func(transport.SubscriptionAck) error { return nil }); err != nil {
		t.Fatal(err)
	}
	session.restoreLifecycle(linkState{generation: 1})
	if got := proxyListenCount(session); got != 1 {
		t.Fatalf("listens after recovery = %d, want 1", got)
	}
	if err := session.notificationStream.forward(notification); err != nil {
		t.Fatal(err)
	}
	tagged := decodeLine[transport.ToolsListChangedNotification](t, out.Bytes())
	if tagged.Method != transport.NotificationToolsChanged || string(tagged.Params.Meta.SubscriptionID) != "9" {
		t.Fatalf("tagged notification = %s", out.Bytes())
	}
	out.Reset()
	other := json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info"}}`)
	if err := session.notificationStream.forward(other); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("modern agent received a notification type it did not subscribe to: %q", out.String())
	}
}

func toolsChangedLine(t *testing.T) json.RawMessage {
	t.Helper()
	line, err := json.Marshal(
		transport.Notification{JsonRPC: transport.JsonRPCVersion, Method: transport.NotificationToolsChanged},
	)
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func TestNotificationStream_forwardDropsInvalidJSON(t *testing.T) {
	var out bytes.Buffer
	stream, _, _ := startNotificationStream(t, &out)
	if err := stream.forward(json.RawMessage(`{"jsonrpc":`)); err != nil || out.Len() != 0 {
		t.Fatalf("invalid notification: error=%v output=%q, want it dropped", err, out.String())
	}
}

func TestNotificationStream_opensAnAuthenticatedDaemonStreamOnlyOnceBound(t *testing.T) {
	t.Run("closed before bind", func(t *testing.T) {
		stream, daemon, _ := startNotificationStream(t, &notificationSink{lines: make(chan string, 1)})
		stream.Close()
		if n := daemon.attempts.Load(); n != 0 {
			t.Fatalf("unbound stream opened %d daemon streams, want none", n)
		}
	})
	for name, token := range map[string]string{"with token": "tok", "no token": ""} {
		t.Run(name, func(t *testing.T) {
			sink := &notificationSink{lines: make(chan string, 1)}
			stream, daemon, _ := startNotificationStream(t, sink)
			stream.Bind(linkState{token: token})
			opened := nextDaemonStream(t, daemon)
			wantAuth := ""
			if token != "" {
				wantAuth = "Bearer " + token
			}
			if opened.header.Get("Accept") != "text/event-stream" || opened.header.Get("Mcp-Session-Id") != "sess" ||
				opened.header.Get("Authorization") != wantAuth {
				t.Fatalf("daemon stream headers = %v, want SSE accept, session sess, auth %q", opened.header, wantAuth)
			}
			opened.send(t, toolsChangedLine(t))
			if got := sink.next(t); got != string(toolsChangedLine(t))+"\n" {
				t.Fatalf("forwarded %q, want the daemon's notification", got)
			}
		})
	}
}

func TestNotificationStream_reconnectsOneSecondAfterTheStreamEnds(t *testing.T) {
	stream, daemon, fake := startNotificationStream(t, &notificationSink{lines: make(chan string, 1)})
	stream.Bind(linkState{token: "tok"})
	nextDaemonStream(t, daemon).end()
	advanceThroughRetryDelay(t, fake, daemon)
	nextDaemonStream(t, daemon)
}

func TestNotificationStream_retriesARejectedStreamWithoutReadingIt(t *testing.T) {
	stream, daemon, fake := startNotificationStream(t, &notificationSink{lines: make(chan string, 1)})
	daemon.status.Store(http.StatusServiceUnavailable)
	stream.Bind(linkState{token: "tok"})
	rejected := nextDaemonStream(t, daemon)
	select {
	case <-rejected.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("rejected stream's body was never closed")
	}
	advanceThroughRetryDelay(t, fake, daemon)
	nextDaemonStream(t, daemon)
}

func advanceThroughRetryDelay(t *testing.T, fake *clock.Fake, daemon *streamDaemon) {
	t.Helper()
	awaitRetryTimer(t, fake)
	fake.Advance(time.Second - time.Millisecond)
	awaitRetryTimer(t, fake)
	select {
	case <-daemon.opened:
		t.Fatal("reconnected before the one-second retry delay")
	default:
	}
	fake.Advance(time.Millisecond)
}

func awaitRetryTimer(t *testing.T, fake *clock.Fake) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := fake.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("no retry timer pending: %v", err)
	}
}

func TestNotificationStream_followsTheNewestLink(t *testing.T) {
	stream, daemon, _ := startNotificationStream(t, &notificationSink{lines: make(chan string, 1)})
	stream.Bind(linkState{token: "old", generation: 1})
	nextDaemonStream(t, daemon)
	stream.Bind(linkState{token: "new", generation: 2})
	if got := nextDaemonStream(t, daemon).header.Get("Authorization"); got != "Bearer new" {
		t.Fatalf("restarted stream authorization = %q, want the new token", got)
	}
	stream.Bind(linkState{token: "old", generation: 1})
	if state, _ := stream.desiredState(); state.token != "new" {
		t.Fatalf("a stale link replaced the current one: %+v", state)
	}
}

func TestNotificationStream_stopsAfterAClientWriteFailure(t *testing.T) {
	sink := &notificationSink{lines: make(chan string, 1), err: errors.New("stdout closed")}
	stream, daemon, _ := startNotificationStream(t, sink)
	stream.Bind(linkState{token: "tok"})
	nextDaemonStream(t, daemon).send(t, toolsChangedLine(t))
	select {
	case <-stream.done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream kept retrying after the agent's output failed")
	}
}

func startNotificationStream(t *testing.T, sink io.Writer) (*notificationStream, *streamDaemon, *clock.Fake) {
	t.Helper()
	daemon := &streamDaemon{opened: make(chan openedDaemonStream, 4)}
	fake := clock.NewFake()
	stream := newNotificationStream(notificationStreamParams{
		Daemon:        DaemonSession{client: &http.Client{Transport: daemon}, sessionID: "sess"},
		Writer:        newLineWriter(sink),
		Clock:         fake,
		Subscriptions: newSubscriptions(),
	})
	t.Cleanup(stream.Close)
	return stream, daemon, fake
}

type streamDaemon struct {
	opened   chan openedDaemonStream
	attempts atomic.Int32
	status   atomic.Int32
}

type openedDaemonStream struct {
	header http.Header
	events *io.PipeWriter
	closed <-chan struct{}
}

type closeRecordingBody struct {
	io.ReadCloser
	once   *sync.Once
	closed chan struct{}
}

func (b closeRecordingBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return b.ReadCloser.Close()
}

func (d *streamDaemon) RoundTrip(req *http.Request) (*http.Response, error) {
	d.attempts.Add(1)
	body, events := io.Pipe()
	context.AfterFunc(req.Context(), func() {
		events.CloseWithError(req.Context().Err()) //nolint:errcheck // CloseWithError on a pipe always succeeds.
	})
	recorded := closeRecordingBody{ReadCloser: body, once: new(sync.Once), closed: make(chan struct{})}
	select {
	case d.opened <- openedDaemonStream{header: req.Header.Clone(), events: events, closed: recorded.closed}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	status := int(d.status.Load())
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Body: recorded, Header: make(http.Header)}, nil
}

func nextDaemonStream(t *testing.T, daemon *streamDaemon) openedDaemonStream {
	t.Helper()
	select {
	case opened := <-daemon.opened:
		return opened
	case <-time.After(5 * time.Second):
		t.Fatal("no daemon notification stream was opened")
		return openedDaemonStream{}
	}
}

func (s openedDaemonStream) send(t *testing.T, message json.RawMessage) {
	t.Helper()
	if _, err := fmt.Fprintf(s.events, "data: %s\n\n", message); err != nil {
		t.Fatal(err)
	}
}

func (s openedDaemonStream) end() {
	s.events.Close() //nolint:errcheck // closing a pipe writer always succeeds.
}

type notificationSink struct {
	lines chan string
	err   error
}

func (s *notificationSink) Write(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	s.lines <- string(p)
	return len(p), nil
}

func (s *notificationSink) next(t *testing.T) string {
	t.Helper()
	select {
	case line := <-s.lines:
		return line
	case <-time.After(5 * time.Second):
		t.Fatal("no notification reached the agent")
		return ""
	}
}
