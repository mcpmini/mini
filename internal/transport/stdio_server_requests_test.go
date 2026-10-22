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
