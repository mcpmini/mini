//go:build test

package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mcpmini/mini/internal/clock"
)

func TestAwaitResponse_ctxCancel(t *testing.T) {
	conn, _, _ := makePipeConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	ch := conn.pending.register(int64(10))
	cancel()

	_, err := conn.awaitResponse(ctx, awaitResponseParams{ID: 10, Method: "initialize", Response: ch})
	if err == nil {
		t.Error("expected error on ctx cancel")
	}
}

func TestAwaitResponse_connClose(t *testing.T) {
	conn, _, _ := makePipeConn(t)
	ch := conn.pending.register(int64(11))
	conn.closeDone()

	_, err := conn.awaitResponse(context.Background(), awaitResponseParams{ID: 11, Method: "ping", Response: ch})
	if err == nil {
		t.Error("expected error when connection closed")
	}
}

func serveOneRequest(w io.Writer, r io.Reader, result any) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := NewScanner(r)
		if !scanner.Scan() {
			return
		}
		var req Request
		json.Unmarshal(scanner.Bytes(), &req)
		sendResponse(w, req.ID, result)
	}()
	return done
}

func TestCall_roundTrip(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	done := serveOneRequest(serverW, serverR, map[string]any{"value": "ok"})

	result, err := conn.Call(context.Background(), "tools/call", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got map[string]any
	json.Unmarshal(result, &got)
	if got["value"] != "ok" {
		t.Errorf("unexpected result: %v", got)
	}
	<-done
}

func TestCall_ctxCancelBeforeResponse(t *testing.T) {
	conn, _, serverR := makePipeConn(t)
	go io.Copy(io.Discard, serverR)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := conn.Call(ctx, "tools/call", nil)
	if err == nil {
		t.Error("expected error for canceled context")
	}
}

func TestStdioPreCancelledCallDoesNotWrite(t *testing.T) {
	var written strings.Builder
	conn := newWriteOnlyConn(discardWriteCloser{Writer: &written})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := conn.Call(ctx, "tools/call", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Call error = %v", err)
	}
	if written.Len() != 0 {
		t.Fatalf("pre-cancelled call wrote %q", written.String())
	}
}

func TestCall_sendsCorrectJSONRPC(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := NewScanner(serverR)
		if !scanner.Scan() {
			return
		}
		var req Request
		json.Unmarshal(scanner.Bytes(), &req)

		sendResponse(serverW, req.ID, nil)
	}()

	conn.Call(context.Background(), "ping", nil)
	<-done
}

func TestListTools_viaPipe(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)

	go func() {
		scanner := NewScanner(serverR)
		if !scanner.Scan() {
			return
		}
		var req Request
		json.Unmarshal(scanner.Bytes(), &req)
		sendResponse(serverW, req.ID, map[string]any{
			"tools": []any{
				map[string]any{"name": "ping", "description": "ping tool", "inputSchema": map[string]any{}},
			},
		})
	}()
	toolList, err := conn.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(toolList.Tools) != 1 || toolList.Tools[0].Name != "ping" {
		t.Errorf("unexpected tools: %+v", toolList.Tools)
	}
}

func TestListTools_viaPipe_pagination(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)

	pages := []map[string]any{
		{"tools": []any{map[string]any{"name": "a", "inputSchema": map[string]any{}}}, "nextCursor": "p2"},
		{"tools": []any{map[string]any{"name": "b", "inputSchema": map[string]any{}}}},
	}
	cursorCh := make(chan string, len(pages))
	go func() {
		scanner := NewScanner(serverR)
		for _, page := range pages {
			if !scanner.Scan() {
				return
			}
			var req Request
			json.Unmarshal(scanner.Bytes(), &req) //nolint:errcheck
			var p struct {
				Cursor string `json:"cursor"`
			}
			json.Unmarshal(req.Params, &p) //nolint:errcheck
			cursorCh <- p.Cursor
			sendResponse(serverW, req.ID, page)
		}
	}()

	got, err := conn.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b"}; !slices.Equal(toolNames(got.Tools), want) {
		t.Errorf("got %v, want %v", toolNames(got.Tools), want)
	}
	gotCursors := []string{<-cursorCh, <-cursorCh}
	if want := []string{"", "p2"}; !slices.Equal(gotCursors, want) {
		t.Errorf("cursors: got %v, want %v", gotCursors, want)
	}
}

const modernContinuationCall = `{"name":"confirm","arguments":{},"inputResponses":{"r1":{"accepted":true}},` +
	`"requestState":9007199254740993,"custom":1,` +
	`"_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{"form":{}}},"unknown":true}}`

func TestStdioModernCallKeepsContinuationFieldsAndMetadata(t *testing.T) {
	conn, serverW, serverR := makePipeConn(t)
	conn.setModern(ModernVersions[0])
	seen := scriptStdioUpstream(serverW, serverR, replyResult(serverW, json.RawMessage(`{"ok":true}`)))
	if _, err := conn.Call(context.Background(), "tools/call", json.RawMessage(modernContinuationCall)); err != nil {
		t.Fatalf("Call: %v", err)
	}
	call := <-seen
	assertRequestMeta(t, requestMetaParams{
		Params:          call.Params,
		ProtocolVersion: ModernVersions[0],
		Elicitation:     true,
	})
	kept := []string{
		`"inputResponses":{"r1":{"accepted":true}}`,
		`"requestState":9007199254740993`,
		`"custom":1`,
		`"unknown":true`,
	}
	for _, raw := range kept {
		if !strings.Contains(string(call.Params), raw) {
			t.Errorf("modern call params %s do not contain %s", call.Params, raw)
		}
	}
}

func TestStdioCancellationTargetsSentRequest(t *testing.T) {
	for _, version := range []string{"", ModernVersions[0]} {
		t.Run("version="+version, func(t *testing.T) { checkStdioCancellation(t, version) })
	}
}

func checkStdioCancellation(t *testing.T, version string) {
	conn, _, serverR := makePipeConn(t)
	conn.setModern(version)
	scanner := bufio.NewScanner(serverR)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := conn.Call(ctx, "tools/call", json.RawMessage(`{"name":"slow","arguments":{}}`))
		result <- err
	}()
	call := readScannerRequest(t, scanner)
	cancel()
	notice := readScannerRequest(t, scanner)
	var cancelled CancelledParams
	if notice.Method != NotificationCancelled || json.Unmarshal(notice.Params, &cancelled) != nil ||
		!sameJSONID(cancelled.RequestID, call.ID) {
		t.Fatalf(
			"after cancelling call %v mini sent %q %s, want a cancel for that call",
			call.ID,
			notice.Method,
			notice.Params,
		)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Call error = %v, want context.Canceled", err)
	}
}

func TestStdioCancellationGivesUpOnItsTurnToWrite(t *testing.T) {
	conn := newWriteOnlyConn(discardWriteCloser{Writer: io.Discard})
	fakeClock := clock.NewFake()
	conn.clock = fakeClock
	if err := conn.lockWrite(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer conn.unlockWrite()
	sent := make(chan struct{})
	go func() {
		conn.cancelUpstreamRequest(context.Background(), 1)
		close(sent)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fakeClock.BlockUntilContext(ctx, 1); err != nil {
		t.Fatal(err)
	}
	fakeClock.Advance(upstreamCancelTimeout)
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("the cancellation kept waiting behind another write")
	}
	if conn.Health(context.Background()) != nil {
		t.Fatal("a cancellation that never started writing ended the connection")
	}
}

func TestStdioCancellationSkipsInitializeAndDeliveredResponses(t *testing.T) {
	for _, method := range []string{"initialize", "tools/call"} {
		t.Run(method, func(t *testing.T) {
			var written strings.Builder
			conn := newWriteOnlyConn(discardWriteCloser{Writer: &written})
			ch := conn.pending.register(1)
			if method != "initialize" {
				conn.pending.deliver(int64(1), &Response{Result: json.RawMessage(`{}`)})
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, _ = conn.awaitResponse(ctx, awaitResponseParams{ID: 1, Method: method, Response: ch})
			if written.Len() != 0 {
				t.Fatalf("unexpected cancellation: %s", written.String())
			}
		})
	}
}

func TestStdioModernCallWithNonObjectParamsSendsNothing(t *testing.T) {
	var written strings.Builder
	conn := newWriteOnlyConn(discardWriteCloser{Writer: &written})
	conn.setModern(ModernVersions[0])
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := conn.Call(ctx, "tools/call", json.RawMessage(`[1]`)); err == nil {
		t.Fatal("modern call with array params succeeded")
	}
	if written.Len() != 0 {
		t.Fatalf("invalid modern call wrote %q to the upstream", written.String())
	}
}

func TestStdioCallReturnsTheUpstreamRPCError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		code    int
		data    json.RawMessage
	}{
		{name: "legacy invalid params", code: CodeInvalidParams},
		{
			name: "modern missing capability", version: ModernVersions[0], code: CodeMissingRequiredClientCapability,
			data: json.RawMessage(`{"requiredCapabilities":{"elicitation":{"form":{}}}}`),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, serverW, serverR := makePipeConn(t)
			conn.setModern(tc.version)
			reject := replyError(serverW, tc.code, tc.data)
			seen := scriptStdioUpstream(serverW, serverR, reject, reject)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			_, err := conn.Call(ctx, "tools/call", json.RawMessage(`{}`))
			var rpcErr *RPCError
			if !errors.As(err, &rpcErr) || rpcErr.Code != tc.code || rpcErr.Message != "rejected" ||
				string(rpcErr.Data) != string(tc.data) {
				t.Fatalf(
					"RPC error = %#v (call error %v), want code %d, message rejected and data %s",
					rpcErr,
					err,
					tc.code,
					tc.data,
				)
			}
			if got := len(seen); got != 1 {
				t.Fatalf("upstream calls = %d, want one without replay", got)
			}
		})
	}
}
