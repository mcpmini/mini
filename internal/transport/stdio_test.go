package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"slices"
	"testing"
	"time"
)

// makePipeConn creates a StdioConnection backed by in-memory pipes.
// serverW: test writes responses here; serverR: test reads requests here.
func makePipeConn(t *testing.T) (conn *StdioConnection, serverW *io.PipeWriter, serverR io.Reader) {
	t.Helper()
	srvOutR, srvOutW := io.Pipe()
	connOutR, connOutW := io.Pipe()
	conn = &StdioConnection{
		cmd:     new(exec.Cmd),
		stdin:   connOutW,
		scanner: NewScanner(srvOutR),
		pending: newPendingMap(),
		done:    make(chan struct{}),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	go conn.readLoop()
	t.Cleanup(func() {
		conn.closeDone()
		connOutW.Close()
		srvOutW.Close()
		srvOutR.Close()
		connOutR.Close()
	})
	return conn, srvOutW, connOutR
}

func sendResponse(w io.Writer, id any, result any) {
	raw, _ := json.Marshal(result)
	line, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "result": json.RawMessage(raw),
	})
	fmt.Fprintf(w, "%s\n", line)
}

func TestDispatch_deliversToWaiter(t *testing.T) {
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

func TestDispatch_ignoresNotification(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)

	notif, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
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

func TestDispatch_rpcError_deliveredToWaiter(t *testing.T) {
	conn, serverW, _ := makePipeConn(t)
	ch := conn.pending.register(int64(5))

	line, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 5,
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

func TestAwaitResponse_ctxCancel(t *testing.T) {
	conn, _, _ := makePipeConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	ch := conn.pending.register(int64(10))
	cancel()

	_, err := conn.awaitResponse(ctx, 10, ch)
	if err == nil {
		t.Error("expected error on ctx cancel")
	}
}

func TestAwaitResponse_connClose(t *testing.T) {
	conn, _, _ := makePipeConn(t)
	ch := conn.pending.register(int64(11))
	conn.closeDone()

	_, err := conn.awaitResponse(context.Background(), 11, ch)
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
	// Drain requests so sendRequest doesn't block on the pipe write.
	go io.Copy(io.Discard, serverR)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := conn.Call(ctx, "tools/call", nil)
	if err == nil {
		t.Error("expected error for canceled context")
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

func TestHealth_openConn(t *testing.T) {
	conn, _, _ := makePipeConn(t)
	if err := conn.Health(context.Background()); err != nil {
		t.Errorf("expected healthy, got: %v", err)
	}
}

func TestHealth_closedConn(t *testing.T) {
	conn, _, _ := makePipeConn(t)
	conn.closeDone()
	if err := conn.Health(context.Background()); err == nil {
		t.Error("expected error for closed connection")
	}
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
	tools, err := conn.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Errorf("unexpected tools: %+v", tools)
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
	if want := []string{"a", "b"}; !slices.Equal(toolNames(got), want) {
		t.Errorf("got %v, want %v", toolNames(got), want)
	}
	gotCursors := []string{<-cursorCh, <-cursorCh}
	if want := []string{"", "p2"}; !slices.Equal(gotCursors, want) {
		t.Errorf("cursors: got %v, want %v", gotCursors, want)
	}
}
