//go:build test

package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/server"
	"github.com/mcpmini/mini/internal/transport"
	"github.com/mcpmini/mini/internal/transport/transporttest"
)

func TestModernNoListenSuppressesNotifications(t *testing.T) {
	for _, message := range serveModern(t, newServerWithRemovableUpstream(t), removeServerRequest(22)) {
		if message.Method == transport.NotificationToolsChanged {
			t.Errorf("tool change sent with no listen open: %s", message.Params)
		}
	}
}

func TestModernListenWithEmptyFilterGetsNoToolChanges(t *testing.T) {
	session := startModernSession(t, newServerWithRemovableUpstream(t))
	session.send(listenRequest(21, transport.ListenNotifications{}))
	if ack := decodeParams[transport.SubscriptionAckParams](t, session.mustNext()); ack.Notifications.ToolsListChanged {
		t.Fatalf("empty filter acknowledged tools-list changes: %+v", ack)
	}
	session.listen(22)
	session.send(removeServerRequest(23))
	changes := session.readChangesUntilResponse(23, 1)
	addChanges(t, changes, session.close())
	if changes["21"] != 0 || changes["22"] != 1 {
		t.Fatalf("tagged notifications per subscription = %v, want only the tools listen", changes)
	}
}

func TestModernConcurrentListensEachReceiveTaggedCopy(t *testing.T) {
	session := startModernSession(t, newServerWithRemovableUpstream(t))
	session.listen(31)
	session.listen(32)
	session.send(removeServerRequest(33))
	changes := session.readChangesUntilResponse(33, 2)
	session.close()
	if changes["31"] != 1 || changes["32"] != 1 || len(changes) != 2 {
		t.Fatalf("tagged notifications per subscription = %v", changes)
	}
}

func TestModernCancelledListenGetsNoFurtherToolChanges(t *testing.T) {
	session := startModernSession(t, newServerWithRemovableUpstream(t))
	session.listen(41)
	session.listen(43)
	session.send(notification(transport.NotificationCancelled, map[string]any{"requestId": 41}))
	session.send(removeServerRequest(44))
	changes := session.readChangesUntilResponse(44, 1)
	rest := session.close()
	addChanges(t, changes, rest)
	if changes["41"] != 0 || changes["43"] != 1 {
		t.Fatalf("tagged notifications per subscription = %v, want only the open listen", changes)
	}
	for _, message := range rest {
		if message.isResponseTo(41) {
			t.Fatalf("cancelled listen got a response: %+v", message)
		}
	}
}

func TestModernListenEOFReturns(t *testing.T) {
	messages := serveModern(t, newTestServer(t, server.Params{}),
		listenRequest(5, transport.ListenNotifications{ToolsListChanged: true}))
	if len(messages) != 1 || messages[0].Method != transport.NotificationSubscriptionsAcknowledged {
		t.Fatalf("expected only acknowledgment at EOF: %+v", messages)
	}
}

func TestModernListenRejectsWrongJSONRPCVersion(t *testing.T) {
	line := bytes.Replace(
		listenRequest(4, transport.ListenNotifications{ToolsListChanged: true}),
		[]byte(`"jsonrpc":"2.0"`), []byte(`"jsonrpc":"1.0"`), 1,
	)
	lines := rawServe(t, newTestServer(t, server.Params{}), line)
	if len(lines) != 1 {
		t.Fatalf("got %d messages, want one error: %s", len(lines), bytes.Join(lines, []byte("\n")))
	}
	if response := decodeMessage(t, lines[0]); response.Error == nil ||
		response.Error.Code != transport.CodeInvalidRequest {
		t.Fatalf("response = %s, want invalid request", lines[0])
	}
}

func newServerWithRemovableUpstream(t *testing.T) *server.Server {
	t.Helper()
	srv := newTestServer(t, server.Params{})
	if err := srv.AddConnection(
		context.Background(),
		config.ServerConfig{Name: "removeme"},
		fakeConn("ping"),
	); err != nil {
		t.Fatal(err)
	}
	return srv
}

func listenRequest(id int, notifications transport.ListenNotifications) []byte {
	return transporttest.ModernRequest(
		id,
		transport.MethodSubscriptionsListen,
		transport.ListenParams{Notifications: notifications},
	)
}

// removeServerRequest makes mini's own tool list change, which is what listeners are notified about.
func removeServerRequest(id int) []byte {
	return transporttest.ModernRequest(id, "tools/call", transport.ToolCallParams{
		Name:      "config",
		Arguments: map[string]any{"action": "remove_server", "server": "removeme"},
	})
}

type modernSession struct {
	t      *testing.T
	ctx    context.Context
	input  *io.PipeWriter
	output *bufio.Reader
	done   chan error
}

func startModernSession(t *testing.T, srv *server.Server) *modernSession {
	t.Helper()
	in, input := io.Pipe()
	outputReader, out := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	// Serve does not return while blocked reading stdin, so a stuck test must close the pipes to fail.
	context.AfterFunc(ctx, func() {
		in.CloseWithError(ctx.Err())           //nolint:errcheck // CloseWithError on a pipe always succeeds.
		outputReader.CloseWithError(ctx.Err()) //nolint:errcheck // CloseWithError on a pipe always succeeds.
	})
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, in, out); out.Close() }()
	return &modernSession{t: t, ctx: ctx, input: input, output: bufio.NewReader(outputReader), done: done}
}

func (s *modernSession) send(line []byte) {
	s.t.Helper()
	if _, err := s.input.Write(line); err != nil {
		s.t.Fatal(err)
	}
}

func (s *modernSession) next() (modernMessage, bool) {
	s.t.Helper()
	line, err := s.output.ReadBytes('\n')
	if s.ctx.Err() != nil {
		s.t.Fatalf("timed out waiting for session output: %v", s.ctx.Err())
	}
	if errors.Is(err, io.EOF) {
		return modernMessage{}, false
	}
	if err != nil {
		s.t.Fatalf("read session output: %v", err)
	}
	return decodeMessage(s.t, line), true
}

func (s *modernSession) mustNext() modernMessage {
	s.t.Helper()
	message, ok := s.next()
	if !ok {
		s.t.Fatal("session output ended")
	}
	return message
}

func (s *modernSession) listen(id int) {
	s.t.Helper()
	s.send(listenRequest(id, transport.ListenNotifications{ToolsListChanged: true}))
	ack := s.mustNext()
	params := decodeParams[transport.SubscriptionAckParams](s.t, ack)
	if ack.Method != transport.NotificationSubscriptionsAcknowledged ||
		string(params.Meta.SubscriptionID) != strconv.Itoa(id) ||
		!params.Notifications.ToolsListChanged {
		s.t.Fatalf("acknowledgment for listen %d = %+v", id, ack)
	}
}

// readChangesUntilResponse counts tools-changed notifications per subscription ID until the response
// to id and the expected number of notifications have arrived; they can interleave in either order.
func (s *modernSession) readChangesUntilResponse(id, wantChanges int) map[string]int {
	s.t.Helper()
	changes := map[string]int{}
	total := 0
	for responded := false; !responded || total < wantChanges; {
		message := s.mustNext()
		responded = responded || message.isResponseTo(id)
		if countToolsChanged(s.t, changes, message) {
			total++
		}
	}
	return changes
}

func addChanges(t *testing.T, changes map[string]int, messages []modernMessage) {
	t.Helper()
	for _, message := range messages {
		countToolsChanged(t, changes, message)
	}
}

func countToolsChanged(t *testing.T, changes map[string]int, message modernMessage) bool {
	t.Helper()
	if message.Method != transport.NotificationToolsChanged {
		return false
	}
	params := decodeParams[transport.SubscriptionNotificationParams](t, message)
	changes[string(params.Meta.SubscriptionID)]++
	return true
}

// close ends the input, waits for Serve to return, and returns every message written after the last read.
func (s *modernSession) close() []modernMessage {
	s.t.Helper()
	s.input.Close() //nolint:errcheck // closing a pipe writer always succeeds.
	var rest []modernMessage
	for message, ok := s.next(); ok; message, ok = s.next() {
		rest = append(rest, message)
	}
	if err := <-s.done; err != nil {
		s.t.Fatal(err)
	}
	return rest
}

func TestLegacyListenWithUnrelatedMetadataRemainsMethodNotFound(t *testing.T) {
	request := legacyRequest(legacyRequestParams{
		T:  t,
		ID: 1, Method: transport.MethodSubscriptionsListen,
		Params: json.RawMessage(`{"_meta":{"trace":"legacy"}}`),
	})
	messages := serveAll(t, newTestServer(t, server.Params{}), request)
	if got := rpcErrorCode(messages[len(messages)-1]); got != transport.CodeMethodNotFound {
		t.Fatalf("legacy listen error code = %v, want -32601", got)
	}
}
