//go:build test

package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestHandleMessage_deliversToWaiter(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)
	ch := conn.pending.register(int64(1))
	sendResponse(serverW, 1, map[string]any{"ok": true})

	select {
	case resp := <-ch:
		if resp.Error != nil {
			t.Errorf("unexpected error: %v", resp.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dispatch")
	}
}

func TestHandleMessage_ignoresNotification(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)

	notif, _ := json.Marshal(map[string]any{
		"jsonrpc": JsonRPCVersion, "method": "notifications/initialized",
	})
	fmt.Fprintf(serverW, "%s\n", notif)

	// A subsequent response to a real pending call should still arrive.
	ch := conn.pending.register(int64(99))
	sendResponse(serverW, 99, "pong")

	select {
	case resp := <-ch:
		if resp.Error != nil {
			t.Errorf("unexpected error: %v", resp.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out — notification may have corrupted dispatch")
	}
}

func TestHandleMessage_buffersNotificationUntilHandlerInstalled(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)
	for range 32 {
		fmt.Fprintln(serverW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
		fmt.Fprintln(serverW, `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`)
	}
	drain := conn.pending.register(int64(99))
	sendResponse(serverW, 99, map[string]any{"ok": true})
	select {
	case <-drain:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pre-handler notifications to drain")
	}

	received := make(chan Notification, 8)
	conn.SetNotificationHandler(func(notification Notification) { received <- notification })
	select {
	case notification := <-received:
		if notification.Method != NotificationToolsChanged {
			t.Fatalf("method = %q", notification.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("notification emitted before handler installation was lost")
	}
	select {
	case notification := <-received:
		t.Fatalf("expected one coalesced pending invalidation, got %q", notification.Method)
	default:
	}

	fmt.Fprintln(serverW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	fmt.Fprintln(serverW, `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`)
	got := []string{
		mustReceiveNotification(t, received).Method,
		mustReceiveNotification(t, received).Method,
	}
	if !slices.Equal(got, []string{NotificationInitialized, NotificationToolsChanged}) {
		t.Fatalf("post-install notifications = %v", got)
	}
}

func mustReceiveNotification(t *testing.T, ch <-chan Notification) Notification {
	t.Helper()
	select {
	case notification := <-ch:
		return notification
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification")
		return Notification{}
	}
}

func TestHandleMessage_rpcError_deliveredToWaiter(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)
	ch := conn.pending.register(int64(5))

	line, _ := json.Marshal(map[string]any{
		"jsonrpc": JsonRPCVersion, "id": 5,
		"error": map[string]any{"code": -32601, "message": "method not found"},
	})
	fmt.Fprintf(serverW, "%s\n", line)

	select {
	case resp := <-ch:
		if resp.Error == nil || resp.Error.Code != -32601 {
			t.Errorf("expected -32601 error, got: %v", resp.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestStdioAnswersServerRequestsDuringACall(t *testing.T) {
	tests := []struct {
		name     string
		modern   bool
		method   string
		wantCode int
	}{
		{name: "legacy ping", method: MethodPing},
		{name: "legacy sampling", method: "sampling/createMessage", wantCode: CodeMethodNotFound},
		{name: "modern ping", modern: true, method: MethodPing, wantCode: CodeMethodNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, serverW, serverR := makePipeConn(t)
			if tt.modern {
				conn.setModern(ModernVersions[0])
			}
			call := startStdioCall(conn)
			lines := readServerLines(serverR)
			request := nextServerRequest(t, lines)
			writeServerRequest(serverW, `"server-health-probe"`, tt.method)

			want := wantReply{id: `"server-health-probe"`, errorCode: tt.wantCode}
			assertServerRequestReply(t, nextServerReply(t, lines), want)
			sendResponse(serverW, request.ID, map[string]any{"tools": []any{}})
			if err := awaitStdioCall(t, call); err != nil {
				t.Fatalf("the call around the server request failed: %v", err)
			}
		})
	}
}

func TestStdioServerRequestSharingACallsIDIsNotItsResponse(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	call := startStdioCall(conn)
	lines := readServerLines(serverR)
	request := nextServerRequest(t, lines)
	callID := string(mustJSONID(t, request.ID))
	writeServerRequest(serverW, callID, "roots/list")

	assertServerRequestReply(t, nextServerReply(t, lines), wantReply{id: callID, errorCode: CodeMethodNotFound})
	select {
	case err := <-call:
		t.Fatalf("the server's request completed mini's call: %v", err)
	default:
	}
	sendResponse(serverW, request.ID, map[string]any{"tools": []any{}})
	if err := awaitStdioCall(t, call); err != nil {
		t.Fatalf("the call's own response failed: %v", err)
	}
}

func TestStdioServerThatStopsReadingStillHasItsResponsesRead(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)
	delivered := conn.pending.register(int64(7))
	go func() {
		writeServerRequest(serverW, `"server-health-probe"`, MethodPing)
		sendResponse(serverW, 7, map[string]any{})
	}()
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("an unread ping reply stopped mini reading the server's output")
	}
}

func TestStdioReplyGivesUpOnItsTurnToWrite(t *testing.T) {
	conn := newWriteOnlyConn(discardWriteCloser{Writer: io.Discard})
	fakeClock := clock.NewFake()
	conn.clock = fakeClock
	if err := conn.lockWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer conn.unlockWrite()
	sent := make(chan struct{})
	go func() {
		reply := Response{JsonRPC: JsonRPCVersion, ID: "server-health-probe", Result: []byte(`{}`)}
		conn.sendReply(context.Background(), reply)
		close(sent)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fakeClock.BlockUntilContext(ctx, 1); err != nil {
		t.Fatal(err)
	}
	fakeClock.Advance(stdioReplyWriteTimeout)
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("the reply kept waiting behind another write")
	}
	if conn.Health(context.Background()) != nil {
		t.Fatal("a reply that never started writing ended the connection")
	}
}

func startStdioCall(conn *StdioConnection) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := conn.Call(context.Background(), "tools/list", nil)
		done <- err
	}()
	return done
}

func writeServerRequest(w io.Writer, id, method string) {
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"method":%q}`+"\n", id, method)
}

func awaitStdioCall(t *testing.T, call <-chan error) error {
	t.Helper()
	select {
	case err := <-call:
		return err
	case <-time.After(time.Second):
		t.Fatal("the call never finished")
		return nil
	}
}

func readServerLines(r io.Reader) <-chan []byte {
	lines := make(chan []byte, 8)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			lines <- slices.Clone(scanner.Bytes())
		}
	}()
	return lines
}

func nextServerLine(t *testing.T, lines <-chan []byte, what string) []byte {
	t.Helper()
	select {
	case line, ok := <-lines:
		if !ok {
			t.Fatalf("stdin closed before mini sent %s", what)
		}
		return line
	case <-time.After(time.Second):
		t.Fatalf("mini never sent %s", what)
		return nil
	}
}

func nextServerRequest(t *testing.T, lines <-chan []byte) Request {
	t.Helper()
	var request Request
	if err := json.Unmarshal(nextServerLine(t, lines, "its request"), &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func nextServerReply(t *testing.T, lines <-chan []byte) Response {
	t.Helper()
	var response Response
	if err := json.Unmarshal(nextServerLine(t, lines, "a reply"), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

type wantReply struct {
	id        string
	errorCode int
}

func assertServerRequestReply(t *testing.T, reply Response, want wantReply) {
	t.Helper()
	if got := string(mustJSONID(t, reply.ID)); got != want.id {
		t.Fatalf("reply id = %s, want %s", got, want.id)
	}
	if want.errorCode == 0 {
		if reply.Error != nil || string(reply.Result) != `{}` {
			t.Fatalf("reply = result %s, error %v; want an empty result", reply.Result, reply.Error)
		}
		return
	}
	if reply.Error == nil || reply.Error.Code != want.errorCode {
		t.Fatalf("reply error = %v, want code %d", reply.Error, want.errorCode)
	}
}
